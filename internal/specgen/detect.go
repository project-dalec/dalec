package specgen

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type RepoFacts struct {
	RepoDir     string
	PrimaryType string // go|rust|node|python|unknown

	TypeCandidates []string

	Name        string
	Description string
	Website     string
	License     string
	Version     string

	HasGoMod         bool
	HasCargoToml     bool
	HasPackageJSON   bool
	HasPyProject     bool
	HasRequirements  bool
	HasSetupPy       bool
	HasMakefile      bool
	HasDockerfile    bool
	HasContainerfile bool

	HasPackageLock bool
	HasPnpmLock    bool
	HasYarnLock    bool

	ReadmePath  string
	LicensePath string

	GoMainRel        string
	GoMainCandidates []string

	NodeHasBuild       bool
	NodeMain           string
	NodeBinName        string
	NodePackageManager string

	CargoPackageName string
	CargoBinName     string

	PythonModuleName    string
	PythonConsoleScript string

	SuggestedSourceGenerator       string
	SuggestedSourceGeneratorSafe   bool
	SuggestedSourceGeneratorReason string
	GoModuleDirective              string
	GoModuleToolchain              string
	GoModuleHasToolBlock           bool

	BuildHints    []CommandHint
	TestHints     []CommandHint
	InstallHints2 []CommandHint
	DocHints      []CommandHint
	MakeTargets   []MakeTargetHint
	Components    []ComponentHint
	ManpagePaths  []string
	ConfigPaths   []string
	Services      []ServiceHint
}

func normalizeManifestDescription(s string) string {
	s = normalizeReadmeLine(s)
	if looksTodo(s) {
		return s
	}
	if len(s) > 220 {
		s = truncateAtWord(s, 220)
	}
	return s
}

func DetectRepo(repoDir string, forcedType string) (*RepoFacts, []string, error) {
	var warnings []string

	f := &RepoFacts{
		RepoDir:     repoDir,
		Name:        sanitizeName(filepath.Base(repoDir)),
		Description: "TODO: describe this package",
		License:     "TODO",
		Version:     "",
	}

	descriptionFromManifest := false

	exists := func(p string) bool {
		_, err := os.Stat(filepath.Join(repoDir, p))
		return err == nil
	}

	f.HasGoMod = exists("go.mod")
	f.HasCargoToml = exists("Cargo.toml")
	f.HasPackageJSON = exists("package.json")
	f.HasPyProject = exists("pyproject.toml")
	f.HasRequirements = exists("requirements.txt")
	f.HasSetupPy = exists("setup.py")
	f.HasMakefile = exists("Makefile") || exists("GNUmakefile") || exists("makefile")
	f.HasDockerfile = exists("Dockerfile")
	f.HasContainerfile = exists("Containerfile")
	f.HasPackageLock = exists("package-lock.json")
	f.HasPnpmLock = exists("pnpm-lock.yaml")
	f.HasYarnLock = exists("yarn.lock")

	if readmePath, readmeText, ok := firstExistingTextFile(
                repoDir,
                []string{"README.md", "README", "README.rst", "README.txt"},
                64*1024,
        ); ok {
                f.ReadmePath = readmePath
                if desc := firstReadmeSentence(readmeText); desc != "" {
                        f.Description = desc
                }
                // website set here only as last resort; go.mod URL takes priority below
                if f.Website == "" {
                        if site := firstUsefulReadmeURL(readmeText); site != "" {
                                f.Website = normalizeRepoURL(site)
                        }
                }
        }

	if licensePath, licenseText, ok := firstExistingTextFile(
		repoDir,
		[]string{"LICENSE", "LICENSE.txt", "LICENSE.md", "COPYING"},
		64*1024,
	); ok {
		f.LicensePath = licensePath
		if lic := detectLicenseFromText(licenseText); lic != "" {
			f.License = lic
		}
	}

	// In DetectRepo, change this block:
	if f.HasGoMod {
                if modPath := parseGoModulePath(repoDir); modPath != "" {
                        if modName := moduleLeafName(modPath); modName != "" && f.Name == sanitizeName(filepath.Base(repoDir)) {
                                f.Name = modName
                        }
                        // go.mod URL is more reliable than README URL — always prefer it
                        if u := modulePathToRepoURL(modPath); u != "" {
                                f.Website = u
                        }
                }
        }


	if f.HasPackageJSON {
		before := f.Description
		enrichFromPackageJSON(repoDir, f)
		if f.Description != before && !looksTodo(f.Description) {
			descriptionFromManifest = true
		}
	}
	if f.HasCargoToml {
		before := f.Description
		enrichFromCargoToml(repoDir, f)
		if f.Description != before && !looksTodo(f.Description) {
			descriptionFromManifest = true
		}
	}
	if f.HasPyProject {
		before := f.Description
		enrichFromPyProject(repoDir, f)
		if f.Description != before && !looksTodo(f.Description) {
			descriptionFromManifest = true
		}
	}
	if f.HasSetupPy && f.PythonModuleName == "" {
		f.PythonModuleName = strings.ReplaceAll(f.Name, "-", "_")
	}

	f.GoMainCandidates = detectGoMainCandidates(repoDir)
	switch len(f.GoMainCandidates) {
	case 1:
		f.GoMainRel = f.GoMainCandidates[0]
	case 0:
	default:
		f.GoMainRel = f.GoMainCandidates[0]
		warnings = append(warnings, fmt.Sprintf("go: multiple main package candidates found; defaulting to %s", f.GoMainRel))
	}

	collectRepoSignals(repoDir, f)

	detected, typeCandidates := detectPrimaryTypeAndCandidates(f)
	f.TypeCandidates = typeCandidates

	if forcedType != "" {
		f.PrimaryType = forcedType
		if detected != "" && detected != "unknown" && forcedType != detected {
			warnings = append(warnings, fmt.Sprintf("repo type forced to %q (auto-detected %q)", forcedType, detected))
		}
	} else {
		f.PrimaryType = detected
	}

	if f.PrimaryType == "unknown" {
		warnings = append(warnings, "could not confidently detect repo type (no strong manifest/build evidence)")
	}

	if f.PrimaryType == "go" {
		switch {
		case f.GoMainRel == "":
			warnings = append(warnings, "go: could not confidently find a main package; baseline build uses '.' and may need adjustment")
			f.GoMainRel = "."
		case f.GoMainRel != ".":
			warnings = append(warnings, fmt.Sprintf("go: using main package at %s", f.GoMainRel))
		}
	}

	if f.PrimaryType == "node" {
		switch {
		case f.HasPnpmLock:
			f.NodePackageManager = "pnpm"
		case f.HasYarnLock:
			f.NodePackageManager = "yarn"
		default:
			f.NodePackageManager = "npm"
		}
	}

	determineSuggestedSourceGenerator(repoDir, f)

	if f.PrimaryType == "python" && f.PythonModuleName == "" {
		f.PythonModuleName = strings.ReplaceAll(f.Name, "-", "_")
	}

	if f.PrimaryType == "rust" && f.CargoBinName == "" && f.CargoPackageName != "" {
		f.CargoBinName = sanitizeName(f.CargoPackageName)
	}

	if strings.TrimSpace(f.Version) == "" {
		f.Version = detectVersionHint(repoDir)
	}

	if !descriptionFromManifest {
		f.Description = normalizeDetectedDescription(f.Description)
	}
	f.Website = normalizeRepoURL(f.Website)

	return f, warnings, nil
}

func detectPrimaryTypeAndCandidates(f *RepoFacts) (string, []string) {
	type scoreItem struct {
		name  string
		score int
	}

	var items []scoreItem

	goScore := 0
	if f.HasGoMod {
		goScore += 10
	}
	if len(f.GoMainCandidates) > 0 {
		goScore += 3 * len(f.GoMainCandidates)
	}
	if goScore > 0 {
		items = append(items, scoreItem{name: "go", score: goScore})
	}

	rustScore := 0
	if f.HasCargoToml {
		rustScore += 10
	}
	if f.CargoBinName != "" {
		rustScore += 3
	}
	if rustScore > 0 {
		items = append(items, scoreItem{name: "rust", score: rustScore})
	}

	nodeScore := 0
	if f.HasPackageJSON {
		nodeScore += 6
	}
	if f.NodeBinName != "" {
		nodeScore += 2
	}
	if f.NodeMain != "" {
		nodeScore += 2
	}
	if f.NodeHasBuild {
		nodeScore += 2
	}
	if nodeScore > 0 {
		items = append(items, scoreItem{name: "node", score: nodeScore})
	}

	pythonScore := 0
	if f.HasPyProject || f.HasRequirements || f.HasSetupPy {
		pythonScore += 6
	}
	if f.PythonConsoleScript != "" {
		pythonScore += 3
	}
	if f.PythonModuleName != "" {
		pythonScore += 1
	}
	if pythonScore > 0 {
		items = append(items, scoreItem{name: "python", score: pythonScore})
	}

	if len(items) == 0 {
		return "unknown", nil
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].score == items[j].score {
			return items[i].name < items[j].name
		}
		return items[i].score > items[j].score
	})

	var out []string
	for _, it := range items {
		out = append(out, it.name)
	}
	return items[0].name, out
}

func collectRepoSignals(repoDir string, f *RepoFacts) {
	f.MakeTargets = parseMakeTargets(repoDir)

	ciHints := collectCICommandHints(repoDir)

	var buildHints []CommandHint
	var testHints []CommandHint
	var installHints []CommandHint
	var docHints []CommandHint

	for _, mt := range f.MakeTargets {
		kind := classifyMakeTarget(mt.Name)
		for _, cmd := range mt.Commands {
			h := CommandHint{
				Command:    strings.TrimSpace(cmd),
				Source:     "Makefile",
				Kind:       kind,
				Confidence: "medium",
				Reason:     "make target command",
			}
			switch kind {
			case "test":
				testHints = append(testHints, h)
			case "install":
				installHints = append(installHints, h)
			case "doc":
				docHints = append(docHints, h)
			default:
				buildHints = append(buildHints, h)
			}
		}
		if len(mt.Commands) == 0 {
			h := CommandHint{
				Command:    "make " + mt.Name,
				Source:     "Makefile",
				Kind:       kind,
				Confidence: "low",
				Reason:     "make target detected",
			}
			switch kind {
			case "test":
				testHints = append(testHints, h)
			case "install":
				installHints = append(installHints, h)
			case "doc":
				docHints = append(docHints, h)
			default:
				buildHints = append(buildHints, h)
			}
		}
	}

	for _, h := range ciHints {
		switch h.Kind {
		case "test":
			testHints = append(testHints, h)
		case "install":
			installHints = append(installHints, h)
		case "doc":
			docHints = append(docHints, h)
		default:
			buildHints = append(buildHints, h)
		}
	}

	f.BuildHints = dedupeCommandHints(buildHints)
	f.TestHints = dedupeCommandHints(testHints)
	f.InstallHints2 = dedupeCommandHints(installHints)
	f.DocHints = dedupeCommandHints(docHints)

	f.ManpagePaths = collectManpagePaths(repoDir)
	f.ConfigPaths = collectConfigPaths(repoDir)
	f.Services = collectServiceHints(repoDir)
	f.Components = collectComponentHints(f)
}

func collectCICommandHints(repoDir string) []CommandHint {
	var out []CommandHint
	roots := []string{
		filepath.Join(repoDir, ".github", "workflows"),
	}

	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			full := filepath.Join(root, e.Name())
			b, err := readSmallTextFile(full, 256*1024)
			if err != nil {
				continue
			}
			out = append(out, extractClassifiedCommandHints(filepath.ToSlash(filepath.Join(".github/workflows", e.Name())), string(b))...)
		}
	}

	for _, name := range []string{"azure-pipelines.yml", ".gitlab-ci.yml"} {
		full := filepath.Join(repoDir, name)
		b, err := readSmallTextFile(full, 256*1024)
		if err != nil {
			continue
		}
		out = append(out, extractClassifiedCommandHints(name, string(b))...)
	}

	return dedupeCommandHints(out)
}

func extractClassifiedCommandHints(source, content string) []CommandHint {
	var out []CommandHint
	for _, raw := range strings.Split(content, "\n") {
		trim := strings.TrimSpace(raw)
		if trim == "" {
			continue
		}
		kind := classifyCommandLine(trim)
		if kind == "" {
			continue
		}
		out = append(out, CommandHint{
			Command:    trim,
			Source:     source,
			Kind:       kind,
			Confidence: "medium",
			Reason:     "command-like CI/build line detected",
		})
	}
	return out
}

func classifyCommandLine(line string) string {
	low := strings.ToLower(strings.TrimSpace(line))
	if low == "" {
		return ""
	}

	switch {
	case strings.HasPrefix(low, "go test"),
		strings.HasPrefix(low, "cargo test"),
		strings.HasPrefix(low, "pytest"),
		strings.HasPrefix(low, "npm test"),
		strings.HasPrefix(low, "pnpm test"),
		strings.HasPrefix(low, "yarn test"),
		strings.Contains(low, " make test"),
		strings.HasPrefix(low, "make test"),
		strings.Contains(low, "make check"),
		strings.Contains(low, "go test "):
		return "test"

	case strings.Contains(low, "install "),
		strings.Contains(low, "mkdir -p"),
		strings.Contains(low, "cp "),
		strings.Contains(low, " mv "),
		strings.HasPrefix(low, "make install"),
		strings.Contains(low, " make install"):
		return "install"

	case strings.Contains(low, "man"),
		strings.Contains(low, "docs"),
		strings.Contains(low, "md2man"),
		strings.Contains(low, "go-md2man"):
		return "doc"

	case strings.Contains(low, "release"),
		strings.Contains(low, "goreleaser"):
		return "release"

	case strings.HasPrefix(low, "go build"),
		strings.HasPrefix(low, "cargo build"),
		strings.HasPrefix(low, "npm "),
		strings.HasPrefix(low, "pnpm "),
		strings.HasPrefix(low, "yarn "),
		strings.HasPrefix(low, "python "),
		strings.HasPrefix(low, "python3 "),
		strings.HasPrefix(low, "pip "),
		strings.HasPrefix(low, "make"),
		strings.Contains(low, " make "):
		return "build"

	default:
		return ""
	}
}

func classifyMakeTarget(name string) string {
	low := strings.ToLower(strings.TrimSpace(name))
	switch {
	case low == "test" || low == "check" || strings.Contains(low, "test"):
		return "test"
	case low == "install" || strings.Contains(low, "install"):
		return "install"
	case low == "man" || low == "docs" || strings.Contains(low, "doc"):
		return "doc"
	case low == "release" || low == "dist" || strings.Contains(low, "release"):
		return "release"
	default:
		return "build"
	}
}

func parseMakeTargets(repoDir string) []MakeTargetHint {
	var full string
	for _, name := range []string{"Makefile", "GNUmakefile", "makefile"} {
		p := filepath.Join(repoDir, name)
		if _, err := os.Stat(p); err == nil {
			full = p
			break
		}
	}
	if full == "" {
		return nil
	}

	b, err := readSmallTextFile(full, 256*1024)
	if err != nil {
		return nil
	}

	lines := strings.Split(string(b), "\n")
	var out []MakeTargetHint
	var current *MakeTargetHint

	flush := func() {
		if current == nil {
			return
		}
		current.Commands = dedupeStrings(current.Commands)
		out = append(out, *current)
		current = nil
	}

	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		trim := strings.TrimSpace(line)
		if trim == "" {
			continue
		}

		if !strings.HasPrefix(line, "\t") && !strings.HasPrefix(line, " ") && strings.Contains(line, ":") && !strings.Contains(line, ":=") {
			left := strings.TrimSpace(strings.SplitN(line, ":", 2)[0])
			if left == "" || strings.Contains(left, "=") || strings.HasPrefix(left, ".") {
				continue
			}
			flush()
			name := strings.Fields(left)[0]
			current = &MakeTargetHint{
				Name:       name,
				Confidence: "medium",
				Reason:     "top-level make target detected",
			}
			continue
		}

		if current != nil && (strings.HasPrefix(line, "\t") || strings.HasPrefix(line, " ")) {
			cmd := strings.TrimSpace(line)
			if cmd != "" && !strings.HasPrefix(cmd, "#") {
				current.Commands = append(current.Commands, cmd)
			}
		}
	}
	flush()

	return dedupeMakeTargetHints(out)
}

func collectManpagePaths(repoDir string) []string {
	var out []string
	roots := []string{"man", "docs/man"}
	for _, root := range roots {
		full := filepath.Join(repoDir, root)
		_ = filepath.Walk(full, func(path string, info os.FileInfo, err error) error {
			if err != nil || info == nil || info.IsDir() {
				return nil
			}
			name := info.Name()
			if strings.HasSuffix(name, ".1") || strings.HasSuffix(name, ".5") || strings.HasSuffix(name, ".8") {
				if rel, err := filepath.Rel(repoDir, path); err == nil {
					out = append(out, filepath.ToSlash(rel))
				}
			}
			return nil
		})
	}
	sort.Strings(out)
	return dedupeStrings(out)
}

func collectConfigPaths(repoDir string) []string {
	var out []string
	roots := []string{"config", "configs", "deploy", "packaging"}
	for _, root := range roots {
		full := filepath.Join(repoDir, root)
		entries, err := os.ReadDir(full)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := strings.ToLower(e.Name())
			if strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".toml") || strings.HasSuffix(name, ".conf") {
				out = append(out, filepath.ToSlash(filepath.Join(root, e.Name())))
			}
		}
	}
	sort.Strings(out)
	return dedupeStrings(out)
}

func collectServiceHints(repoDir string) []ServiceHint {
	var out []ServiceHint
	paths := []string{
		"systemd",
		"packaging/systemd",
		"deploy/systemd",
	}
	for _, root := range paths {
		full := filepath.Join(repoDir, root)
		entries, err := os.ReadDir(full)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".service") {
				continue
			}
			out = append(out, ServiceHint{
				Name:       strings.TrimSuffix(e.Name(), ".service"),
				UnitPath:   filepath.ToSlash(filepath.Join(root, e.Name())),
				Confidence: "medium",
			})
		}
	}
	return dedupeServiceHints(out)
}

// internalToolName returns true for cmd/ subdirectory names that are
// build-time codegen or internal tooling, not installable package artifacts.
func internalToolName(name string) bool {
        internal := []string{
                "gen-", "protoc-", "go-build", "go-plugin",
                "mockgen", "stringer", "wire",
        }
        low := strings.ToLower(strings.TrimSpace(name))
        for _, prefix := range internal {
                if strings.HasPrefix(low, prefix) {
                        return true
                }
        }
        return false
}

func collectComponentHints(f *RepoFacts) []ComponentHint {
	var out []ComponentHint
	add := func(name, path, language, role, confidence, reason string) {
		name = sanitizeName(name)
		if name == "" || name == "unknown" {
			return
		}
		out = append(out, ComponentHint{
			Name:       name,
			Path:       path,
			Language:   language,
			Role:       role,
			Confidence: confidence,
			Reason:     reason,
		})
	}

	for _, rel := range f.GoMainCandidates {
		name := filepath.Base(rel)
		if rel == "." || rel == "" {
			switch {
			case sanitizeName(f.Name) != "" && sanitizeName(f.Name) != "unknown":
				name = f.Name
			default:
				name = filepath.Base(f.RepoDir)
			}
		}
		add(name, rel, "go", "cli", "medium", "go main package detected")
	}

	if f.CargoBinName != "" {
		add(f.CargoBinName, "", "rust", "cli", "medium", "cargo binary detected")
	}
	if f.NodeBinName != "" {
		add(f.NodeBinName, "", "node", "cli", "medium", "node bin entry detected")
	}
	if f.PythonConsoleScript != "" {
		add(f.PythonConsoleScript, "", "python", "cli", "high", "python console script detected")
	}
	if len(f.Services) > 0 {
		for _, s := range f.Services {
			add(s.Name, s.UnitPath, f.PrimaryType, "daemon", "medium", "systemd service detected")
		}
	}
	if len(out) == 0 && f.Name != "" {
		add(f.Name, "", f.PrimaryType, "cli", "low", "repository name fallback")
	}

	return dedupeComponentHints(out)
}

func dedupeCommandHints(in []CommandHint) []CommandHint {
	seen := map[string]struct{}{}
	out := make([]CommandHint, 0, len(in))
	for _, item := range in {
		key := item.Kind + "|" + item.Source + "|" + item.Command
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func dedupeMakeTargetHints(in []MakeTargetHint) []MakeTargetHint {
	seen := map[string]struct{}{}
	out := make([]MakeTargetHint, 0, len(in))
	for _, item := range in {
		key := item.Name
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func dedupeComponentHints(in []ComponentHint) []ComponentHint {
	seen := map[string]struct{}{}
	out := make([]ComponentHint, 0, len(in))
	for _, item := range in {
		key := item.Name + "|" + item.Path + "|" + item.Role
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func dedupeServiceHints(in []ServiceHint) []ServiceHint {
	seen := map[string]struct{}{}
	out := make([]ServiceHint, 0, len(in))
	for _, item := range in {
		key := item.Name + "|" + item.UnitPath
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func readSmallTextFile(path string, maxBytes int) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if maxBytes > 0 && len(b) > maxBytes {
		b = b[:maxBytes]
	}
	return b, nil
}

func enrichFromPackageJSON(repoDir string, f *RepoFacts) {
	type repoField struct {
		URL string `json:"url"`
	}
	type pkg struct {
		Name        string            `json:"name"`
		Version     string            `json:"version"`
		License     string            `json:"license"`
		Description string            `json:"description"`
		Homepage    string            `json:"homepage"`
		Repository  any               `json:"repository"`
		Scripts     map[string]string `json:"scripts"`
		Main        string            `json:"main"`
		Bin         any               `json:"bin"`
	}

	b, err := os.ReadFile(filepath.Join(repoDir, "package.json"))
	if err != nil {
		return
	}
	var p pkg
	if err := json.Unmarshal(b, &p); err != nil {
		return
	}

	if p.Name != "" {
		f.Name = sanitizeName(p.Name)
	}
	if p.Version != "" {
		f.Version = p.Version
	}
	if p.License != "" {
		f.License = p.License
	}
	if p.Description != "" {
		f.Description = normalizeManifestDescription(p.Description)
	}
	if p.Homepage != "" {
		f.Website = normalizeRepoURL(p.Homepage)
	}

	switch r := p.Repository.(type) {
	case string:
		if f.Website == "" {
			f.Website = normalizeRepoURL(r)
		}
	case map[string]any:
		if f.Website == "" {
			if u, ok := r["url"].(string); ok && u != "" {
				f.Website = normalizeRepoURL(u)
			}
		}
	case repoField:
		if f.Website == "" && r.URL != "" {
			f.Website = normalizeRepoURL(r.URL)
		}
	}

	if _, ok := p.Scripts["build"]; ok {
		f.NodeHasBuild = true
	}
	if p.Main != "" {
		f.NodeMain = p.Main
	}
	switch b := p.Bin.(type) {
	case string:
		f.NodeBinName = sanitizeName(f.Name)
	case map[string]any:
		var names []string
		for k := range b {
			names = append(names, sanitizeName(k))
		}
		sort.Strings(names)
		if len(names) > 0 {
			f.NodeBinName = names[0]
		}
	}
}

func enrichFromCargoToml(repoDir string, f *RepoFacts) {
	b, err := os.ReadFile(filepath.Join(repoDir, "Cargo.toml"))
	if err != nil {
		return
	}
	lines := strings.Split(string(b), "\n")
	section := ""
	for _, raw := range lines {
		line := cleanTOMLLine(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line)
			continue
		}
		key, val, ok := parseTOMLKV(line)
		if !ok {
			continue
		}
		switch section {
		case "[package]":
			switch key {
			case "name":
				f.CargoPackageName = sanitizeName(val)
				f.Name = sanitizeName(val)
			case "version":
				f.Version = val
			case "description":
				f.Description = normalizeManifestDescription(val)
			case "license":
				f.License = val
			case "homepage", "repository":
				if f.Website == "" {
					f.Website = normalizeRepoURL(val)
				}
			}
		case "[[bin]]":
			if key == "name" && f.CargoBinName == "" {
				f.CargoBinName = sanitizeName(val)
			}
		}
	}
}

func enrichFromPyProject(repoDir string, f *RepoFacts) {
	b, err := os.ReadFile(filepath.Join(repoDir, "pyproject.toml"))
	if err != nil {
		return
	}
	lines := strings.Split(string(b), "\n")
	section := ""
	for _, raw := range lines {
		line := cleanTOMLLine(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line)
			continue
		}
		key, val, ok := parseTOMLKV(line)
		if !ok {
			continue
		}
		switch section {
		case "[project]":
			switch key {
			case "name":
				f.Name = sanitizeName(val)
				f.PythonModuleName = strings.ReplaceAll(sanitizeName(val), "-", "_")
			case "version":
				f.Version = val
			case "description":
				f.Description = normalizeManifestDescription(val)
			case "license":
				if val != "" {
					f.License = val
				}
			}
		case "[project.urls]":
			if strings.EqualFold(key, "homepage") && f.Website == "" {
				f.Website = normalizeRepoURL(val)
			}
			if strings.EqualFold(key, "repository") && f.Website == "" {
				f.Website = normalizeRepoURL(val)
			}
		case "[project.scripts]":
			if f.PythonConsoleScript == "" && key != "" {
				f.PythonConsoleScript = sanitizeName(key)
			}
		}
	}
}

func determineSuggestedSourceGenerator(repoDir string, f *RepoFacts) {
	if f == nil {
		return
	}

	switch f.PrimaryType {
	case "go":
		inspectGoModuleForBaseline(repoDir, f)
	case "rust":
		if f.HasCargoToml {
			f.SuggestedSourceGenerator = "cargohome"
			f.SuggestedSourceGeneratorSafe = true
			f.SuggestedSourceGeneratorReason = "Cargo manifest detected"
		}
	case "node":
		if f.HasPackageJSON {
			f.SuggestedSourceGenerator = "node-mod"
			f.SuggestedSourceGeneratorSafe = true
			f.SuggestedSourceGeneratorReason = "package.json detected"
		}
	case "python":
		if f.HasPyProject || f.HasRequirements || f.HasSetupPy {
			f.SuggestedSourceGenerator = "pip"
			f.SuggestedSourceGeneratorSafe = true
			f.SuggestedSourceGeneratorReason = "Python packaging metadata detected"
		}
	}
}

func inspectGoModuleForBaseline(repoDir string, f *RepoFacts) {
	f.SuggestedSourceGenerator = "gomod"
	f.SuggestedSourceGeneratorSafe = false

	b, err := os.ReadFile(filepath.Join(repoDir, "go.mod"))
	if err != nil {
		f.SuggestedSourceGeneratorReason = "go.mod could not be read"
		return
	}

	f.SuggestedSourceGeneratorSafe = true
	f.SuggestedSourceGeneratorReason = "go.mod uses a conservative language-version shape"

	for _, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}

		switch {
		case strings.HasPrefix(line, "go "):
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				f.GoModuleDirective = strings.TrimSpace(fields[1])
				if !isConservativeGoLanguageVersion(f.GoModuleDirective) {
					f.SuggestedSourceGeneratorSafe = false
					f.SuggestedSourceGeneratorReason = fmt.Sprintf("go.mod uses non-conservative go directive %q", f.GoModuleDirective)
				}
			}
		case strings.HasPrefix(line, "toolchain "):
			f.GoModuleToolchain = strings.TrimSpace(strings.TrimPrefix(line, "toolchain "))
			f.SuggestedSourceGeneratorSafe = false
			f.SuggestedSourceGeneratorReason = "go.mod uses a toolchain directive"
		case line == "tool (" || strings.HasPrefix(line, "tool ("):
			f.GoModuleHasToolBlock = true
			f.SuggestedSourceGeneratorSafe = false
			f.SuggestedSourceGeneratorReason = "go.mod uses a tool block"
		}
	}
}

func isConservativeGoLanguageVersion(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	return regexp.MustCompile(`^\d+\.\d+$`).MatchString(v)
}

func parseGoModulePath(repoDir string) string {
	b, err := os.ReadFile(filepath.Join(repoDir, "go.mod"))
	if err != nil {
		return ""
	}
	for _, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return ""
}

func moduleLeafName(modPath string) string {
	modPath = strings.TrimSpace(modPath)
	if modPath == "" {
		return ""
	}
	parts := strings.Split(modPath, "/")
	return sanitizeName(parts[len(parts)-1])
}

func modulePathToRepoURL(modPath string) string {
	modPath = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(modPath, "https://"), ".git"))
	modPath = strings.TrimPrefix(modPath, "http://")
	modPath = strings.TrimPrefix(modPath, "git+")
	if modPath == "" {
		return ""
	}

	parts := strings.Split(modPath, "/")
	if len(parts) < 3 {
		return ""
	}

	host := parts[0]
	switch host {
	case "github.com", "gitlab.com", "bitbucket.org":
		return "https://" + strings.Join(parts[:3], "/")
	default:
		return ""
	}
}

func detectGoMainCandidates(repoDir string) []string {
	var candidates []string

	hasMainPackage := func(dir string) bool {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return false
		}

		sawPackageMain := false
		sawFuncMain := false

		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}

			b, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				continue
			}
			s := string(b)

			if strings.Contains(s, "package main") {
				sawPackageMain = true
			}
			if strings.Contains(s, "func main(") {
				sawFuncMain = true
			}
		}

		return sawPackageMain && sawFuncMain
	}

	if hasMainPackage(repoDir) {
		candidates = append(candidates, ".")
	}

	cmdDir := filepath.Join(repoDir, "cmd")
	entries, err := os.ReadDir(cmdDir)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			subdir := filepath.Join(cmdDir, e.Name())
			if hasMainPackage(subdir) {
				candidates = append(candidates, filepath.ToSlash(filepath.Join("cmd", e.Name())))
			}
		}
	}

	sort.Strings(candidates)
	return candidates
}

func firstExistingTextFile(repoDir string, names []string, maxBytes int) (string, string, bool) {
	for _, name := range names {
		b, err := os.ReadFile(filepath.Join(repoDir, name))
		if err != nil {
			continue
		}
		if maxBytes > 0 && len(b) > maxBytes {
			b = b[:maxBytes]
		}
		return name, string(b), true
	}
	return "", "", false
}

func isBadgeLikeReadmeLine(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}

	if strings.HasPrefix(line, "[![") || strings.HasPrefix(line, "![") {
		return true
	}

	badgeHints := []string{
		"shields.io",
		"goreportcard.com",
		"/actions/workflows/",
		"/actions/runs/",
		"badge.svg",
		"img.shields.io",
	}
	for _, h := range badgeHints {
		if strings.Contains(line, h) {
			return true
		}
	}
	return false
}

func firstReadmeSentence(s string) string {
        inCodeFence := false
        for _, raw := range strings.Split(s, "\n") {
                line := strings.TrimSpace(raw)

                if strings.HasPrefix(line, "```") {
                        inCodeFence = !inCodeFence
                        continue
                }
                if inCodeFence {
                        continue
                }

                if line == "" || strings.HasPrefix(line, "#") {
                        continue
                }
                if isBadgeLikeReadmeLine(line) {
                        continue
                }
                if strings.HasPrefix(line, "|") || strings.HasPrefix(line, "---") {
                        continue
                }
                // skip HTML tags
                if strings.HasPrefix(line, "<") {
                        continue
                }
		line = normalizeReadmeLine(line)
		if line == "" {
			continue
		}
		if len(line) < 20 {
			continue
		}

		if len(line) > 220 {
			line = truncateAtWord(line, 220)
		}
		if !strings.HasSuffix(line, ".") {
			line += "."
		}
		return line
	}
	return ""
}

func firstUsefulReadmeURL(s string) string {
	for _, raw := range strings.Split(s, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if isBadgeLikeReadmeLine(line) {
			continue
		}
		for _, u := range allURLs(line) {
			if isUsefulProjectURL(u) {
				return normalizeRepoURL(u)
			}
		}
	}
	return ""
}

func allURLs(s string) []string {
	var out []string
	for _, tok := range strings.Fields(s) {
		tok = strings.Trim(tok, "()<>{}[],'\".")
		if strings.HasPrefix(tok, "https://") || strings.HasPrefix(tok, "http://") {
			out = append(out, tok)
		}
	}
	return out
}

func isUsefulProjectURL(u string) bool {
        lu := strings.ToLower(strings.TrimSpace(u))
        if lu == "" {
                return false
        }

        bad := []string{
                "shields.io",
                "img.shields.io",
                "goreportcard.com",
                "/actions/workflows/",
                "/actions/runs/",
                "badge.svg",
                ".svg",
                ".png",
                ".jpg",
                ".jpeg",
                ".gif",
                "macports",
                "homebrew",
                "chocolatey",
                "winget",
                "pkg.go.dev",
                "godoc.org",
                "msys2.org",
                "repology.org",
                "snapcraft.io",
                "/blob/",
        }
	for _, x := range bad {
		if strings.Contains(lu, x) {
			return false
		}
	}
	return strings.HasPrefix(lu, "https://") || strings.HasPrefix(lu, "http://")
}

func normalizeRepoURL(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "git+")
	s = strings.TrimSuffix(s, ".git")
	return s
}

func normalizeReadmeLine(line string) string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "- ")
	line = strings.TrimPrefix(line, "* ")
	line = strings.TrimPrefix(line, "+ ")
	line = strings.TrimPrefix(line, "> ")
	line = strings.TrimSpace(line)

	line = strings.ReplaceAll(line, "`", "")
	line = strings.ReplaceAll(line, "**", "")
	line = strings.ReplaceAll(line, "__", "")

	re := regexp.MustCompile(`\[(.*?)\]\((.*?)\)`)
	line = re.ReplaceAllString(line, "$1")

	line = strings.TrimSpace(strings.Trim(line, "*_-'\""))
	line = collapseSpaces(line)
	return line
}

func normalizeDetectedDescription(s string) string {
	s = normalizeReadmeLine(s)
	if looksTodo(s) {
		return s
	}
	if len(s) > 220 {
		s = truncateAtWord(s, 220)
	}
	if s != "" && !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}

func detectLicenseFromText(s string) string {
	upper := strings.ToUpper(s)
	switch {
	case strings.Contains(upper, "APACHE LICENSE") && strings.Contains(upper, "VERSION 2.0"):
		return "Apache-2.0"
	case strings.Contains(upper, "MIT LICENSE"):
		return "MIT"
	case strings.Contains(upper, "GNU GENERAL PUBLIC LICENSE"):
		return "GPL"
	case strings.Contains(upper, "BSD"):
		return "BSD"
	default:
		return ""
	}
}

func cleanTOMLLine(line string) string {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return ""
	}
	if idx := strings.Index(line, "#"); idx >= 0 {
		line = strings.TrimSpace(line[:idx])
	}
	return line
}

func parseTOMLKV(line string) (key, value string, ok bool) {
	parts := strings.SplitN(line, "=", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	key = strings.TrimSpace(parts[0])
	value = strings.TrimSpace(parts[1])
	value = strings.Trim(value, "\"'")
	return key, value, key != ""
}

func truncateAtWord(s string, maxLen int) string {
	if maxLen <= 0 || len(s) <= maxLen {
		return s
	}
	cut := s[:maxLen]
	if idx := strings.LastIndex(cut, " "); idx >= 40 {
		cut = cut[:idx]
	}
	return strings.TrimSpace(cut)
}

func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func sanitizeName(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.TrimPrefix(s, "@")
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, "_", "-")
	s = strings.ReplaceAll(s, " ", "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-")
	if s == "" {
		return "unknown"
	}
	return s
}

func detectVersionHint(repoDir string) string {
	for _, name := range []string{"VERSION", "version", "version.txt", ".version"} {
		b, err := os.ReadFile(filepath.Join(repoDir, name))
		if err != nil {
			continue
		}
		if v := normalizeDetectedVersion(string(b)); v != "" {
			return v
		}
	}

	if v := detectVersionFromGit(repoDir); v != "" {
		return v
	}

	return ""
}

func detectVersionFromGit(repoDir string) string {
	cmd := exec.Command("git", "-C", repoDir, "describe", "--tags", "--abbrev=0")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return normalizeDetectedVersion(string(out))
}

func normalizeDetectedVersion(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if strings.Contains(s, "\n") {
		s = strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
	}
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")
	if looksVersionLike(s) {
		return s
	}
	return ""
}

func looksVersionLike(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	re := regexp.MustCompile(`^[0-9]+(\.[0-9]+){1,3}([\-+][A-Za-z0-9._-]+)?([\-+][A-Za-z0-9._-]+)?$`)
	return re.MatchString(s)
}
