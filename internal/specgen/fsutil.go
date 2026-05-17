package specgen

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const defaultPerFileContextLimit = 32 * 1024

type scoredCandidate struct {
	Path  string
	Score int
}

func readSmallFile(repoDir, rel string, limit int) ([]byte, error) {
	if limit <= 0 {
		return nil, nil
	}
	p := filepath.Join(repoDir, rel)
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	if len(b) > limit {
		return b[:limit], nil
	}
	return b, nil
}

func listFiles(repoDir, relDir string, max int) ([]string, error) {
	dir := filepath.Join(repoDir, relDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	var out []string
	for _, n := range names {
		if max > 0 && len(out) >= max {
			break
		}
		out = append(out, filepath.ToSlash(filepath.Join(relDir, n)))
	}
	return out, nil
}

func CollectRepoContextFiles(facts *RepoFacts, analysis *Analysis, unresolved []UnresolvedItem, maxFiles, maxBytes int) ([]AIFile, []string) {
	if maxFiles <= 0 {
		maxFiles = 60
	}
	if maxBytes <= 0 {
		maxBytes = 300 * 1024
	}
	var warnings []string
	if facts == nil {
		return nil, []string{"AI context had no repo facts; file selection skipped"}
	}
	repoDir := facts.RepoDir
	candidates := map[string]int{}
	addCandidate := func(rel string, score int) {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "" {
			return
		}
		st, err := os.Stat(filepath.Join(repoDir, rel))
		if err != nil || st.IsDir() {
			return
		}
		if cur, ok := candidates[rel]; !ok || score > cur {
			candidates[rel] = score
		}
	}

	for _, item := range []struct {
		path  string
		score int
	}{
		{"Makefile", 150}, {"GNUmakefile", 150}, {"makefile", 150},
		{"Taskfile.yml", 145}, {"Taskfile.yaml", 145}, {"justfile", 145},
		{"azure-pipelines.yml", 142}, {".gitlab-ci.yml", 141}, {".cirrus.yml", 141},
		{"README.md", 138}, {"README.rst", 138}, {"README.txt", 138},
		{"LICENSE", 136}, {"LICENSE.txt", 136}, {"COPYING", 136},
		{"Dockerfile", 132}, {"Containerfile", 132},
		{"go.mod", 134}, {"go.sum", 122}, {"Cargo.toml", 134}, {"Cargo.lock", 122},
		{"package.json", 134}, {"package-lock.json", 122}, {"pnpm-lock.yaml", 122}, {"yarn.lock", 122},
		{"pyproject.toml", 134}, {"setup.py", 130}, {"requirements.txt", 128},
	} {
		addCandidate(item.path, item.score)
	}
	addCandidate(facts.ReadmePath, 139)
	addCandidate(facts.LicensePath, 137)

	if analysis != nil {
		for _, ev := range analysis.Evidence {
			addCandidate(ev.Source, 143)
		}
		for _, p := range analysis.ManpagePaths {
			addCandidate(p, 120)
		}
		for _, p := range analysis.ConfigPaths {
			addCandidate(p, 122)
		}
		for _, svc := range analysis.Services {
			addCandidate(svc.UnitPath, 126)
			addCandidate(svc.ConfigPath, 121)
		}
		for _, rel := range facts.GoMainCandidates {
			if rel == "." {
				addCandidate("main.go", 124)
			} else {
				addCandidate(filepath.ToSlash(filepath.Join(rel, "main.go")), 124)
			}
		}
		if facts.NodeMain != "" {
			addCandidate(facts.NodeMain, 122)
		}
		if facts.PythonModuleName != "" {
			addCandidate(filepath.ToSlash(strings.ReplaceAll(facts.PythonModuleName, ".", "/")+".py"), 120)
		}
	}

	for _, item := range unresolved {
		switch item.Code {
		case "build.multiple_drivers", "dependencies.build", "build.steps", "repo.unknown_type", "artifacts.make_output_unknown", "artifacts.multi_bin_selection", "image.entrypoint":
			addCandidate("Makefile", 150)
			addCandidate("README.md", 138)
			addCandidate("azure-pipelines.yml", 142)
		}
	}

	for _, dir := range []struct {
		name  string
		max   int
		score int
	}{
		{".github/workflows", 20, 143}, {"docs", 20, 114}, {"cmd", 30, 118},
		{"scripts", 24, 120}, {"hack", 20, 111}, {"build", 20, 112}, {"pkg", 20, 96},
		{"internal", 20, 96}, {"src", 20, 96}, {"packaging", 24, 126}, {"deploy", 20, 122},
		{"dist", 20, 122}, {"contrib", 20, 116}, {"systemd", 12, 127}, {"man", 20, 124},
	} {
		walkImportantDir(repoDir, candidates, dir.name, dir.max, dir.score)
	}

	var ordered []scoredCandidate
	for p, s := range candidates {
		ordered = append(ordered, scoredCandidate{Path: p, Score: s})
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Score == ordered[j].Score {
			return ordered[i].Path < ordered[j].Path
		}
		return ordered[i].Score > ordered[j].Score
	})

	var out []AIFile
	used := 0
	for _, c := range ordered {
		if len(out) >= maxFiles || used >= maxBytes {
			break
		}
		perFileLimit := defaultPerFileContextLimit
		if remaining := maxBytes - used; remaining < perFileLimit {
			perFileLimit = remaining
		}
		if perFileLimit <= 0 {
			break
		}
		b, err := readSmallFile(repoDir, c.Path, perFileLimit*3)
		if err != nil || len(b) == 0 {
			continue
		}
		content := strings.TrimSpace(shrinkContextFile(c.Path, string(b), perFileLimit))
		if content == "" {
			continue
		}
		used += len(content)
		out = append(out, AIFile{Path: c.Path, Content: content})
	}
	if len(out) == 0 {
		warnings = append(warnings, "AI context had no readable files (repo may be empty or selection too strict)")
	}
	return out, warnings
}

func walkImportantDir(repoDir string, out map[string]int, relDir string, max int, score int) {
	root := filepath.Join(repoDir, relDir)
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return
	}
	interestingBase := map[string]struct{}{"Makefile": {}, "GNUmakefile": {}, "makefile": {}, "Taskfile.yml": {}, "Taskfile.yaml": {}, "justfile": {}, "Dockerfile": {}, "Containerfile": {}, "package.json": {}, "Cargo.toml": {}, "go.mod": {}, "pyproject.toml": {}, "README.md": {}, "README.rst": {}, "README.txt": {}}
	interestingExts := []string{".go", ".rs", ".py", ".js", ".mjs", ".cjs", ".ts", ".tsx", ".sh", ".ps1", ".service", ".yml", ".yaml", ".json", ".toml", ".mk", ".md", ".rst", ".txt", ".1", ".5", ".8", ".conf"}
	count := 0
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if count >= max {
			return fs.SkipAll
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(repoDir, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		base := filepath.Base(path)
		if _, ok := interestingBase[base]; ok || hasInterestingExt(path, interestingExts) {
			if cur, ok := out[rel]; !ok || score > cur {
				out[rel] = score
			}
			count++
		}
		return nil
	})
}

func hasInterestingExt(path string, exts []string) bool {
	low := strings.ToLower(path)
	for _, ext := range exts {
		if strings.HasSuffix(low, strings.ToLower(ext)) {
			return true
		}
	}
	return false
}

func shrinkContextFile(rel, content string, limit int) string {
	if limit <= 0 {
		return ""
	}
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	content = strings.TrimSpace(content)
	if content == "" || len(content) <= limit {
		return content
	}
	lowRel := strings.ToLower(filepath.ToSlash(rel))
	base := filepath.Base(lowRel)
	switch {
	case strings.Contains(lowRel, ".github/workflows/"), base == "azure-pipelines.yml", base == ".gitlab-ci.yml", base == ".cirrus.yml", base == "makefile", base == "gnumakefile", base == "taskfile.yml", base == "taskfile.yaml", base == "justfile", base == "dockerfile", base == "containerfile", strings.HasSuffix(base, ".service"):
		return headTail(content, limit)
	case strings.HasSuffix(base, ".md"), strings.HasSuffix(base, ".rst"), strings.HasSuffix(base, ".txt"), strings.HasPrefix(base, "readme"), base == "license", base == "license.txt", base == "copying":
		return extractRelevantText(content, limit, []string{"build", "install", "usage", "run", "quickstart", "compile", "binary", "output", "entrypoint", "command", "test", "systemd", "service"})
	default:
		return headTail(content, limit)
	}
}

func extractRelevantText(content string, limit int, keywords []string) string {
	lines := strings.Split(content, "\n")
	selected := map[int]struct{}{}
	for i := 0; i < len(lines) && i < 80; i++ {
		selected[i] = struct{}{}
	}
	for i, line := range lines {
		if !containsAnyFold(line, keywords) {
			continue
		}
		start := max(0, i-2)
		end := min(len(lines)-1, i+4)
		for j := start; j <= end; j++ {
			selected[j] = struct{}{}
		}
	}
	var idxs []int
	for i := range selected {
		idxs = append(idxs, i)
	}
	sort.Ints(idxs)
	var b strings.Builder
	last := -2
	for _, i := range idxs {
		if last >= 0 && i > last+1 {
			b.WriteString("\n...\n")
		}
		b.WriteString(lines[i])
		b.WriteString("\n")
		last = i
	}
	return headTail(strings.TrimSpace(b.String()), limit)
}

func headTail(content string, limit int) string {
	if limit <= 0 || len(content) <= limit {
		return content
	}
	if limit < 64 {
		return content[:limit]
	}
	head := (limit * 2) / 3
	tail := limit - head - len("\n...\n")
	if tail < 16 {
		tail = 16
		if head > limit-tail-len("\n...\n") {
			head = limit - tail - len("\n...\n")
		}
	}
	if head+tail+len("\n...\n") >= len(content) {
		return content[:limit]
	}
	return content[:head] + "\n...\n" + content[len(content)-tail:]
}

func containsAnyFold(s string, needles []string) bool {
	ls := strings.ToLower(s)
	for _, n := range needles {
		if strings.Contains(ls, strings.ToLower(n)) {
			return true
		}
	}
	return false
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
