package specgen

import (
	"os"
	"strings"
)

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func looksSemanticMajorOnly(s string) bool {
	s = strings.TrimSpace(strings.ToLower(s))
	if len(s) < 2 || s[0] != 'v' {
		return false
	}
	for _, ch := range s[1:] {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func fileContains(path string, needle string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(b), needle)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func hasDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func boolToString(v bool) string {
	if v {
		return "1"
	}
	return ""
}

func nonEmptyCount(vals ...string) []string {
	var out []string
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}

func looksTodo(s string) bool {
	s = strings.TrimSpace(strings.ToLower(s))
	return s == "" || s == "todo" || strings.Contains(s, "todo:")
}

func selectStrategy(f *RepoFacts, a *Analysis) string {
	switch {
	case f.HasGoMod && f.HasMakefile:
		if len(f.GoMainCandidates) > 1 {
			return "go-make-multi-bin"
		}
		return "go-make"

	case f.HasGoMod && len(f.GoMainCandidates) > 1:
		return "go-multi-bin"

	case f.HasGoMod:
		return "go-simple"

	case f.HasCargoToml && cargoWorkspace(f.RepoDir):
		return "rust-workspace"

	case f.HasCargoToml:
		return "rust-simple"

	case f.HasPackageJSON:
		switch f.NodePackageManager {
		case "pnpm":
			return "node-pnpm-app"
		case "yarn":
			return "node-yarn-app"
		default:
			return "node-npm-app"
		}

	case f.HasPyProject || f.HasSetupPy:
		return "python-wheel"

	case f.HasRequirements:
		return "python-requirements"

	case f.HasMakefile:
		return "generic-make"

	default:
		_ = a
		return "generic-placeholder"
	}
}

func trimGoModuleMajorSuffix(s string) string {
	s = strings.TrimSpace(strings.Trim(s, "/"))
	if s == "" {
		return ""
	}
	parts := strings.Split(s, "/")
	if len(parts) == 0 {
		return s
	}
	last := strings.ToLower(parts[len(parts)-1])
	if looksSemanticMajorOnly(last) && len(parts) > 1 {
		return strings.Join(parts[:len(parts)-1], "/")
	}
	return s
}

func canonicalRepoName(name string) string {
	name = sanitizeName(name)
	if name == "" {
		return ""
	}
	if looksSemanticMajorOnly(name) {
		return ""
	}
	return name
}

func isLikelyBuildHelperBinary(name string) bool {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return false
	}
	if strings.HasPrefix(name, "protoc-gen-") {
		return true
	}
	if strings.HasPrefix(name, "gen-") {
		return true
	}
	switch name {
	case "go-buildtag", "gen-manpages":
		return true
	default:
		return false
	}
}
