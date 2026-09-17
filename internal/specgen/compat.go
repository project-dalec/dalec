package specgen

import (
	"path/filepath"
	"strings"
)

// AIFile is a compact file payload used by the optional AI-refinement path.
// The baseline generator keeps this type even when AI is disabled so the
// package remains source-compatible as the feature set evolves.
type AIFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func dedupeWarnings(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, w := range in {
		w = strings.TrimSpace(w)
		if w == "" {
			continue
		}
		if _, ok := seen[w]; ok {
			continue
		}
		seen[w] = struct{}{}
		out = append(out, w)
	}
	return out
}

func normalizeArtifactPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}

	path = strings.ReplaceAll(path, "\\", "/")

	// Keep artifact paths repo-relative and stable in emitted YAML/tests.
	for strings.HasPrefix(path, "./") {
		path = strings.TrimPrefix(path, "./")
	}
	path = strings.TrimPrefix(path, "/")

	cleaned := filepath.ToSlash(filepath.Clean(path))
	if cleaned == "." {
		return ""
	}
	for strings.HasPrefix(cleaned, "./") {
		cleaned = strings.TrimPrefix(cleaned, "./")
	}
	return cleaned
}

func looksDangerous(cmd string) bool {
	cmd = strings.ToLower(strings.TrimSpace(cmd))
	if cmd == "" {
		return false
	}

	dangerous := []string{
		"curl | sh",
		"curl|sh",
		"wget | sh",
		"wget|sh",
		"curl -s",
		"curl -fs",
		"curl -sf",
		"bash -c \"$(curl",
		"sh -c \"$(curl",
		"bash <(curl",
		"sh <(curl",
		"nc -e ",
		"mkfifo ",
		"rm -rf /",
		"sudo rm -rf /",
	}

	for _, needle := range dangerous {
		if strings.Contains(cmd, needle) {
			return true
		}
	}

	return false
}
