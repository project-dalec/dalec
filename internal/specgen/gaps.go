package specgen

import (
	"fmt"
	"strings"

	"github.com/project-dalec/dalec"
)

func FindSpecGaps(a *Analysis, spec *dalec.Spec) []UnresolvedItem {
	var out []UnresolvedItem

	if spec == nil {
		return []UnresolvedItem{{
			Code:     "spec.nil",
			Message:  "generated spec is nil",
			Severity: "high",
		}}
	}

	if strings.TrimSpace(spec.Name) == "" {
		out = append(out, UnresolvedItem{
			Code:     "spec.name",
			Message:  "spec name is empty",
			Severity: "high",
		})
	}
	if strings.TrimSpace(spec.Version) == "" {
		out = append(out, UnresolvedItem{
			Code:     "spec.version",
			Message:  "spec version is empty",
			Severity: "high",
		})
	}
	if strings.TrimSpace(spec.Revision) == "" {
		out = append(out, UnresolvedItem{
			Code:     "spec.revision",
			Message:  "spec revision is empty",
			Severity: "high",
		})
	}
	if len(spec.Sources) == 0 {
		out = append(out, UnresolvedItem{
			Code:     "spec.sources",
			Message:  "spec has no sources",
			Severity: "high",
		})
	}

	if looksTodo(spec.Description) {
		out = append(out, UnresolvedItem{
			Code:     "description",
			Message:  "spec description is missing or placeholder",
			Severity: "low",
		})
	}
	if looksTodo(spec.License) {
		out = append(out, UnresolvedItem{
			Code:     "license",
			Message:  "spec license is missing or placeholder",
			Severity: "medium",
		})
	}
	if strings.TrimSpace(spec.Website) == "" {
		out = append(out, UnresolvedItem{
			Code:     "website",
			Message:  "spec website is missing",
			Severity: "low",
		})
	}

	if shouldExpectBuildSteps(a) {
		if len(spec.Build.Steps) == 0 {
			out = append(out, UnresolvedItem{
				Code:     "build.steps",
				Message:  "build steps are missing for a baseline that appears to require a build",
				Severity: "high",
			})
		} else {
			for i, s := range spec.Build.Steps {
				if strings.TrimSpace(s.Command) == "" {
					out = append(out, UnresolvedItem{
						Code:     "build.empty_step",
						Message:  fmt.Sprintf("build step %d is empty", i),
						Severity: "high",
					})
				}
			}
		}
	}

	if shouldExpectBinaryArtifacts(a) && len(spec.Artifacts.Binaries) == 0 {
		out = append(out, UnresolvedItem{
			Code:     "artifacts.binaries",
			Message:  "analysis suggests binary outputs but no binary artifacts were emitted",
			Severity: "high",
		})
	}
	if shouldExpectConfigArtifacts(a) && len(spec.Artifacts.ConfigFiles) == 0 {
		out = append(out, UnresolvedItem{
			Code:     "artifacts.config",
			Message:  "analysis suggests config files but no config artifacts were emitted",
			Severity: "medium",
		})
	}
	if shouldExpectManpages(a) && len(spec.Artifacts.Manpages) == 0 {
		out = append(out, UnresolvedItem{
			Code:     "artifacts.manpages",
			Message:  "analysis suggests manpages but none were emitted",
			Severity: "medium",
		})
	}
	if shouldExpectLibexec(a) && len(spec.Artifacts.Libexec) == 0 {
		out = append(out, UnresolvedItem{
			Code:     "artifacts.libexec",
			Message:  "analysis suggests libexec files but none were emitted",
			Severity: "medium",
		})
	}
	if shouldExpectSystemd(a) {
		if spec.Artifacts.Systemd == nil || spec.Artifacts.Systemd.IsEmpty() {
			out = append(out, UnresolvedItem{
				Code:     "artifacts.systemd",
				Message:  "analysis suggests systemd units but none were emitted",
				Severity: "medium",
			})
		}
	}

	return dedupeUnresolved(out)
}

func shouldExpectBinaryArtifacts(a *Analysis) bool {
	if a == nil {
		return false
	}
	for _, art := range a.Artifacts {
		if strings.TrimSpace(art.Kind) == "binary" {
			return true
		}
	}
	return false
}

func shouldExpectConfigArtifacts(a *Analysis) bool {
	if a == nil {
		return false
	}
	for _, art := range a.Artifacts {
		if strings.TrimSpace(art.Kind) == "config" {
			return true
		}
	}
	return false
}

func shouldExpectManpages(a *Analysis) bool {
	if a == nil {
		return false
	}
	for _, art := range a.Artifacts {
		if strings.TrimSpace(art.Kind) == "manpage" {
			return true
		}
	}
	return false
}

func shouldExpectLibexec(a *Analysis) bool {
	if a == nil {
		return false
	}
	for _, art := range a.Artifacts {
		if strings.TrimSpace(art.Kind) == "libexec" {
			return true
		}
	}
	return false
}

func shouldExpectSystemd(a *Analysis) bool {
	if a == nil {
		return false
	}
	for _, art := range a.Artifacts {
		if strings.TrimSpace(art.Kind) == "systemd" {
			return true
		}
	}
	return false
}

func shouldExpectBuildSteps(a *Analysis) bool {
	if a == nil {
		return true
	}

	switch a.SelectedStrategy {
	case "generic-placeholder", "container-assembly":
		return false
	}

	if looksContainerOnlyAnalysis(a) {
		return false
	}

	return true
}

func shouldExpectBuildDeps(a *Analysis) bool {
	if a == nil {
		return true
	}

	switch a.SelectedStrategy {
	case "generic-placeholder", "container-assembly":
		return false
	default:
		return !looksContainerOnlyAnalysis(a)
	}
}

func shouldExpectArtifacts(a *Analysis) bool {
	if a == nil {
		return true
	}

	switch a.SelectedStrategy {
	case "generic-placeholder":
		return false
	default:
		return true
	}
}

func shouldExpectTests(a *Analysis) bool {
	if a == nil {
		return true
	}

	if looksContainerOnlyAnalysis(a) {
		return false
	}

	if a.Runtime.Kind != "unknown" && a.Runtime.Kind != "library" {
		return true
	}

	switch a.SelectedStrategy {
	case "go-simple", "go-make", "go-multi-bin", "go-make-multi-bin", "rust-simple", "rust-workspace", "python-wheel", "python-requirements", "node-npm-app", "node-yarn-app", "node-pnpm-app":
		return true
	default:
		return false
	}
}

func looksContainerOnlyAnalysis(a *Analysis) bool {
	if a == nil {
		return false
	}

	if a.SelectedStrategy == "container-assembly" {
		return true
	}

	if a.Facts == nil {
		return false
	}

	hasLanguageProject := a.Facts.HasGoMod ||
		a.Facts.HasCargoToml ||
		a.Facts.HasPackageJSON ||
		a.Facts.HasPyProject ||
		a.Facts.HasRequirements ||
		a.Facts.HasSetupPy

	if (a.Facts.HasDockerfile || a.Facts.HasContainerfile) && !hasLanguageProject {
		return true
	}

	return false
}

func looksPlaceholderBuildCommand(cmd string) bool {
	low := strings.ToLower(strings.TrimSpace(cmd))
	return strings.Contains(low, "todo: add build steps") ||
		strings.Contains(low, "placeholder build") ||
		strings.Contains(low, "echo 'container-only intent") ||
		strings.Contains(low, `echo "container-only intent`)
}

func countArtifacts(a dalec.Artifacts) int {
	return len(a.Binaries) +
		len(a.Docs) +
		len(a.DataDirs) +
		len(a.Manpages) +
		len(a.ConfigFiles)
}

func dedupeUnresolved(in []UnresolvedItem) []UnresolvedItem {
	seen := map[string]struct{}{}
	out := make([]UnresolvedItem, 0, len(in))
	for _, item := range in {
		key := item.Code + "|" + item.Message + "|" + item.Severity
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}
