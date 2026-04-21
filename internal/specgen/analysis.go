package specgen

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func AnalyzeRepo(f *RepoFacts) (*Analysis, []string) {
	var warnings []string
	if f == nil {
		a := &Analysis{
			SelectedStrategy:    "generic-placeholder",
			SelectedRuntimeKind: "unknown",
			Confidence: ConfidenceReport{
				Metadata: "low",
				Strategy: "low",
				Runtime:  "low",
				Overall:  "low",
			},
			Unresolved: []UnresolvedItem{
				{
					Code:     "analysis.nil_facts",
					Message:  "repo facts were nil; falling back to generic placeholder strategy",
					Severity: "high",
				},
			},
			Alternatives: &Alternatives{
				BuildStyles: []ScoredChoice{
					{
						Value:      "generic-placeholder",
						Score:      10,
						Confidence: "low",
						Reason:     "repo facts were nil",
					},
				},
			},
			Decisions: []DecisionRecord{
				{
					Field:      "build_style",
					Chosen:     "generic-placeholder",
					Confidence: "low",
					Reason:     "repo facts were nil",
				},
			},
		}
		return a, warnings
	}

	a := &Analysis{
		Facts: f,
		Metadata: MetadataHint{
			Name:        f.Name,
			Description: f.Description,
			Website:     f.Website,
			License:     f.License,
			Version:     f.Version,
		},
		SelectedRuntimeKind: "unknown",
	}

	addUnique := func(dst *[]string, vals ...string) {
		seen := map[string]struct{}{}
		for _, x := range *dst {
			seen[x] = struct{}{}
		}
		for _, v := range vals {
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			if _, ok := seen[v]; ok {
				continue
			}
			seen[v] = struct{}{}
			*dst = append(*dst, v)
		}
	}

	addEvidence := func(kind, source, detail, confidence string) {
		a.Evidence = append(a.Evidence, Evidence{
			Kind:       kind,
			Source:     source,
			Detail:     detail,
			Confidence: normalizeConfidence(confidence),
		})
	}

	if f.HasGoMod {
		addUnique(&a.Languages, "go")
		addUnique(&a.BuildDrivers, "go")
		addEvidence("manifest", "go.mod", "Go module detected", "high")
		if !f.SuggestedSourceGeneratorSafe && strings.TrimSpace(f.SuggestedSourceGeneratorReason) != "" {
			addEvidence("generator", "go.mod", "optional gomod source generator skipped: "+strings.TrimSpace(f.SuggestedSourceGeneratorReason), "low")
		}
	}
	if f.HasCargoToml {
		addUnique(&a.Languages, "rust")
		addUnique(&a.BuildDrivers, "cargo")
		addEvidence("manifest", "Cargo.toml", "Cargo manifest detected", "high")
	}
	if f.HasPackageJSON {
		addUnique(&a.Languages, "node")
		switch f.NodePackageManager {
		case "pnpm":
			addUnique(&a.BuildDrivers, "pnpm")
		case "yarn":
			addUnique(&a.BuildDrivers, "yarn")
		default:
			addUnique(&a.BuildDrivers, "npm")
		}
		addEvidence("manifest", "package.json", "Node package manifest detected", "high")
	}
	if f.HasPyProject || f.HasRequirements || f.HasSetupPy {
		addUnique(&a.Languages, "python")
		addUnique(&a.BuildDrivers, "pip")
		addEvidence("manifest", "python", "Python build metadata detected", "medium")
	}
	if f.HasMakefile {
		addUnique(&a.BuildDrivers, "make")
		addEvidence("buildfile", "Makefile", "Top-level Makefile detected", "high")
	}
	if f.HasDockerfile {
		addUnique(&a.PackageHints, "dockerfile")
		addEvidence("buildfile", "Dockerfile", "Container build file detected", "medium")
	}
	if f.HasContainerfile {
		addUnique(&a.PackageHints, "containerfile")
		addEvidence("buildfile", "Containerfile", "Container build file detected", "medium")
	}
	if fileExists(filepath.Join(f.RepoDir, "azure-pipelines.yml")) {
		addUnique(&a.PackageHints, "azure-pipelines")
		addEvidence("ci", "azure-pipelines.yml", "Azure Pipelines file detected", "medium")
	}
	if hasDir(filepath.Join(f.RepoDir, ".github", "workflows")) {
		addUnique(&a.PackageHints, "github-actions")
		addEvidence("ci", ".github/workflows", "GitHub Actions workflow directory detected", "medium")
	}

	// Discover richer signals before strategy/runtime/artifact selection.
	a.CandidateComponents = discoverCandidateComponents(f)
	a.Services = discoverServices(f)
	a.ManpagePaths = discoverManpages(f)
	a.ConfigPaths = discoverConfigPaths(f)
	a.CICommands = discoverCICommands(f)
	a.InstallHints = discoverInstallHints(f)
	a.BuildHints, a.TestHints, a.InstallHints2, a.DocHints, a.MakeTargets, a.Components = deriveStructuredHints(f, a)
	
	        // Normalize legacy string install hints from structured hints.
        if len(a.InstallHints2) > 0 {
                a.InstallHints = commandHintsToStrings(a.InstallHints2)
        }

	a.RepoShape = inferRepoShape(f)
	a.SelectedStrategy = selectStrategy(f, a)
	a.Runtime = inferRuntime(f, a)
	a.SelectedRuntimeKind = a.Runtime.Kind

	a.InstallLayout = deriveInstallLayout(f, a)
	a.Artifacts = inferArtifacts(f, a)
	a.Alternatives = &Alternatives{
		Components:   scoreComponentChoices(f, a),
		BuildStyles:  scoreBuildStyleChoices(f, a),
		EntryPoints:  scoreEntrypointChoices(f, a),
		PackageNames: scorePackageNameChoices(f, a),
	}

	a.Decisions = deriveDecisionRecords(f, a)
	a.Confidence = inferConfidence(f, a)

	a.Unresolved = deriveAnalysisUnresolved(f, a)
	if len(a.Unresolved) > 0 {
		warnings = append(warnings, "analysis found unresolved areas; baseline preserved structured uncertainty for refinement")
	}

	return a, warnings
}

func deriveStructuredHints(f *RepoFacts, a *Analysis) ([]CommandHint, []CommandHint, []CommandHint, []CommandHint, []MakeTargetHint, []ComponentHint) {
	var buildHints []CommandHint
	var testHints []CommandHint
	var installHints []CommandHint
	var docHints []CommandHint
	var makeTargets []MakeTargetHint
	var components []ComponentHint

	if f != nil {
		buildHints = append(buildHints, f.BuildHints...)
		testHints = append(testHints, f.TestHints...)
		installHints = append(installHints, f.InstallHints2...)
		docHints = append(docHints, f.DocHints...)
		makeTargets = append(makeTargets, f.MakeTargets...)
		components = append(components, f.Components...)
	}

	// Backfill from legacy string hints only if structured hints are sparse.
	if len(buildHints) == 0 && a != nil {
		for _, line := range a.CICommands {
			switch classifyAnalysisCommandLine(line) {
			case "test":
				testHints = append(testHints, CommandHint{
					Command:    line,
					Source:     "analysis.ci",
					Kind:       "test",
					Confidence: "low",
					Reason:     "derived from CI command line",
				})
			case "install":
				installHints = append(installHints, CommandHint{
					Command:    line,
					Source:     "analysis.ci",
					Kind:       "install",
					Confidence: "low",
					Reason:     "derived from CI command line",
				})
			case "doc":
				docHints = append(docHints, CommandHint{
					Command:    line,
					Source:     "analysis.ci",
					Kind:       "doc",
					Confidence: "low",
					Reason:     "derived from CI command line",
				})
			case "build", "release":
				buildHints = append(buildHints, CommandHint{
					Command:    line,
					Source:     "analysis.ci",
					Kind:       "build",
					Confidence: "low",
					Reason:     "derived from CI command line",
				})
			}
		}
	}

	if len(installHints) == 0 && a != nil {
		for _, line := range a.InstallHints {
			installHints = append(installHints, CommandHint{
				Command:    line,
				Source:     "analysis.install",
				Kind:       "install",
				Confidence: "low",
				Reason:     "derived from install-like command line",
			})
		}
	}

	if len(components) == 0 && a != nil {
		for _, name := range a.CandidateComponents {
			components = append(components, ComponentHint{
				Name:       sanitizeName(name),
				Role:       "cli",
				Confidence: "low",
				Reason:     "candidate component fallback",
			})
		}
		for _, s := range a.Services {
			components = append(components, ComponentHint{
				Name:       sanitizeName(s.Name),
				Path:       s.UnitPath,
				Role:       "daemon",
				Confidence: "medium",
				Reason:     "service hint fallback",
			})
		}
	}

	return dedupeCommandHints(buildHints),
		dedupeCommandHints(testHints),
		dedupeCommandHints(installHints),
		dedupeCommandHints(docHints),
		dedupeMakeTargetHints(makeTargets),
		dedupeComponentHints(components)
}

func classifyAnalysisCommandLine(line string) string {
	low := strings.ToLower(strings.TrimSpace(line))
	switch {
	case strings.HasPrefix(low, "go test"),
		strings.HasPrefix(low, "cargo test"),
		strings.HasPrefix(low, "pytest"),
		strings.Contains(low, "make test"):
		return "test"
	case strings.Contains(low, "install "),
		strings.Contains(low, "mkdir -p"),
		strings.Contains(low, "cp "),
		strings.Contains(low, "make install"):
		return "install"
	case strings.Contains(low, "doc"),
		strings.Contains(low, "man"),
		strings.Contains(low, "md2man"):
		return "doc"
	case strings.Contains(low, "release"):
		return "release"
	case strings.HasPrefix(low, "go build"),
		strings.HasPrefix(low, "cargo build"),
		strings.HasPrefix(low, "npm "),
		strings.HasPrefix(low, "pnpm "),
		strings.HasPrefix(low, "yarn "),
		strings.HasPrefix(low, "make"):
		return "build"
	default:
		return ""
	}
}

func inferRepoShape(f *RepoFacts) RepoShape {
	shape := RepoShape{
		PrimarySubdir: ".",
	}
	if f.GoMainRel != "" {
		shape.PrimarySubdir = f.GoMainRel
	}
	if len(f.GoMainCandidates) > 0 {
		shape.CandidateSubdirs = append([]string(nil), f.GoMainCandidates...)
	}
	if len(f.GoMainCandidates) > 1 {
		shape.HasMultipleBins = true
		shape.HasMultipleApps = true
	}
	if cargoWorkspace(f.RepoDir) || nodeWorkspace(f.RepoDir) {
		shape.HasWorkspace = true
	}
	if shape.HasWorkspace || len(nonEmptyCount(
		boolToString(f.HasGoMod),
		boolToString(f.HasCargoToml),
		boolToString(f.HasPackageJSON),
		boolToString(f.HasPyProject || f.HasRequirements || f.HasSetupPy),
	)) > 1 || shape.HasMultipleApps {
		shape.IsMonorepo = true
	}
	return shape
}

func inferRuntime(f *RepoFacts, a *Analysis) RuntimeHint {
	if a != nil && len(a.Services) > 0 {
		entry := sanitizeName(a.Services[0].Name)
		if entry == "" || entry == "unknown" {
			entry = sanitizeName(f.Name)
		}
		return RuntimeHint{
			Kind:       "daemon",
			Entrypoint: entry,
			Cmd:        "",
			Confidence: "medium",
		}
	}

	if a != nil {
		for _, c := range a.Components {
			if c.Role == "daemon" {
				return RuntimeHint{
					Kind:       "daemon",
					Entrypoint: sanitizeName(c.Name),
					Confidence: normalizeConfidence(c.Confidence),
				}
			}
		}
		for _, c := range a.Components {
			if c.Role == "cli" {
				cmd := ""
				if f != nil && (f.PrimaryType == "go" || f.PrimaryType == "rust") {
					cmd = "--help"
				}
				return RuntimeHint{
					Kind:       "cli",
					Entrypoint: sanitizeName(c.Name),
					Cmd:        cmd,
					Confidence: normalizeConfidence(c.Confidence),
				}
			}
		}
	}

	switch f.PrimaryType {
	case "go":
		entry := sanitizeName(f.Name)
		if entry == "" || entry == "unknown" {
			entry = "app"
		}
		return RuntimeHint{
			Kind:       "cli",
			Entrypoint: entry,
			Cmd:        "--help",
			Confidence: "medium",
		}
	case "rust":
		entry := sanitizeName(f.Name)
		if f.CargoBinName != "" {
			entry = sanitizeName(f.CargoBinName)
		}
		return RuntimeHint{
			Kind:       "cli",
			Entrypoint: entry,
			Cmd:        "--help",
			Confidence: "medium",
		}
	case "node":
		switch {
		case f.NodeMain != "":
			return RuntimeHint{
				Kind:       "cli",
				Entrypoint: "node",
				Cmd:        f.NodeMain,
				Confidence: "medium",
			}
		case f.NodeBinName != "":
			return RuntimeHint{
				Kind:       "cli",
				Entrypoint: f.NodeBinName,
				Confidence: "medium",
			}
		default:
			return RuntimeHint{
				Kind:       "unknown",
				Confidence: "low",
			}
		}
	case "python":
		switch {
		case f.PythonConsoleScript != "":
			return RuntimeHint{
				Kind:       "cli",
				Entrypoint: f.PythonConsoleScript,
				Confidence: "high",
			}
		case f.PythonModuleName != "":
			return RuntimeHint{
				Kind:       "cli",
				Entrypoint: "python3",
				Cmd:        "-m " + f.PythonModuleName,
				Confidence: "medium",
			}
		default:
			return RuntimeHint{
				Kind:       "unknown",
				Confidence: "low",
			}
		}
	default:
		return RuntimeHint{
			Kind:       "unknown",
			Confidence: "low",
		}
	}
}

func inferArtifacts(f *RepoFacts, a *Analysis) []ArtifactHint {
	var out []ArtifactHint

	addArtifact := func(kind, name, path, confidence, reason string) {
		out = append(out, ArtifactHint{
			Kind:       kind,
			Name:       name,
			Path:       path,
			Confidence: normalizeConfidence(confidence),
			Reason:     strings.TrimSpace(reason),
		})
	}

	if a != nil {
		for _, pe := range a.InstallLayout.Binaries {
			name := sanitizeName(filepath.Base(pe.Path))
			if name == "" || name == "unknown" {
				name = sanitizeName(a.Runtime.Entrypoint)
			}
			addArtifact("binary", name, pe.Path, pe.Confidence, pe.Reason)
		}
		for _, pe := range a.InstallLayout.Manpages {
			addArtifact("manpage", filepath.Base(pe.Path), pe.Path, pe.Confidence, pe.Reason)
		}
		for _, pe := range a.InstallLayout.ConfigFiles {
			addArtifact("config", filepath.Base(pe.Path), pe.Path, pe.Confidence, pe.Reason)
		}
		for _, pe := range a.InstallLayout.Docs {
			addArtifact("doc", filepath.Base(pe.Path), pe.Path, pe.Confidence, pe.Reason)
		}
		for _, pe := range a.InstallLayout.DataDirs {
			addArtifact("data", filepath.Base(pe.Path), pe.Path, pe.Confidence, pe.Reason)
		}
		for _, pe := range a.InstallLayout.Libexec {
			addArtifact("libexec", filepath.Base(pe.Path), pe.Path, pe.Confidence, pe.Reason)
		}
	}

	// Fallback artifacts when install-layout evidence is still sparse.
	switch f.PrimaryType {
	case "go":
		if !hasArtifactKind(out, "binary") {
			name := sanitizeName(f.Name)
			if a != nil && strings.TrimSpace(a.Runtime.Entrypoint) != "" && a.Runtime.Entrypoint != "node" && a.Runtime.Entrypoint != "python3" {
				name = sanitizeName(a.Runtime.Entrypoint)
			}
			addArtifact("binary", name, nativeBinaryArtifactPath(name), "medium", "fallback Go binary artifact path")
		}
	case "rust":
		if !hasArtifactKind(out, "binary") {
			name := sanitizeName(f.Name)
			if f.CargoBinName != "" {
				name = sanitizeName(f.CargoBinName)
			}
			addArtifact("binary", name, "src/target/release/"+name, "medium", "fallback Cargo release artifact path")
		}
	case "node":
		if !hasArtifactKind(out, "doc") {
			addArtifact("doc", "README", "src/README.md", "low", "fallback Node documentation artifact")
		}
	case "python":
		if !hasArtifactKind(out, "wheel") {
			addArtifact("wheel", sanitizeName(f.Name), "src/dist", "medium", "fallback Python wheel/dist artifact")
		}
	default:
		if len(out) == 0 {
			addArtifact("unknown", "", "", "low", "no strong artifact evidence found")
		}
	}

	return dedupeArtifactHints(out)
}

func inferConfidence(f *RepoFacts, a *Analysis) ConfidenceReport {
	c := ConfidenceReport{
		Metadata: "medium",
		Strategy: "medium",
		Runtime:  normalizeConfidence(a.Runtime.Confidence),
		Overall:  "medium",
	}

	if looksTodo(a.Metadata.Description) || looksTodo(a.Metadata.License) {
		c.Metadata = "low"
	}
	if a.Metadata.Website != "" && !looksTodo(a.Metadata.Description) && !looksTodo(a.Metadata.License) {
		c.Metadata = "high"
	}

	switch a.SelectedStrategy {
	case "go-simple", "rust-simple":
		c.Strategy = "high"
	case "go-make", "node-npm-app", "node-yarn-app", "node-pnpm-app", "python-wheel", "python-requirements", "rust-workspace", "go-multi-bin", "go-make-multi-bin":
		c.Strategy = "medium"
	default:
		c.Strategy = "low"
	}

	if len(a.Alternatives.BuildStyles) > 0 {
		c.Strategy = normalizeConfidence(a.Alternatives.BuildStyles[0].Confidence)
	}

	if f.PrimaryType == "unknown" || a.SelectedStrategy == "generic-placeholder" {
		c.Overall = "low"
		return c
	}
	if c.Metadata == "high" && c.Strategy == "high" && c.Runtime != "low" {
		c.Overall = "high"
		return c
	}
	if c.Metadata == "low" || c.Strategy == "low" || c.Runtime == "low" {
		c.Overall = "low"
		return c
	}
	c.Overall = "medium"
	return c
}

func deriveAnalysisUnresolved(f *RepoFacts, a *Analysis) []UnresolvedItem {
	var out []UnresolvedItem

	if f.PrimaryType == "unknown" {
		out = append(out, UnresolvedItem{
			Code:     "repo.unknown_type",
			Message:  "could not confidently classify the repository type",
			Severity: "high",
			Suggestions: []string{
				"inspect build/test scripts and CI files",
				"preserve the baseline bundle so refinement can use richer repository context",
			},
		})
	}
	
	if f.PrimaryType == "go" && f.GoNeedsManagedToolchain {
	if strings.TrimSpace(f.GoManagedToolchainVersion) == "" {
		out = append(out, UnresolvedItem{
			Code:     "build.go_toolchain_version_unknown",
			Message:  "repo requires an explicit Go toolchain, but the required version could not be derived from go.mod",
			Severity: "high",
			Suggestions: []string{
				"inspect go.mod go/toolchain directives",
				"set an explicit managed Go version in planning",
			},
		})
	} else {
		out = append(out, UnresolvedItem{
			Code:     "build.go_toolchain_managed",
			Message:  "repo requires an explicit Go toolchain; baseline will select it automatically",
			Severity: "low",
			Suggestions: []string{
				"emit a managed Go toolchain in the deterministic build plan",
			},
		})
	}
}



	if f.PrimaryType == "go" && f.HasGoMod && !f.SuggestedSourceGeneratorSafe && strings.TrimSpace(f.SuggestedSourceGeneratorReason) != "" {
		out = append(out, UnresolvedItem{
			Code:     "sources.generate.gomod_skipped",
			Message:  "baseline skipped the optional gomod source generator: " + strings.TrimSpace(f.SuggestedSourceGeneratorReason),
			Severity: "low",
			Suggestions: []string{
				"keep the baseline on direct go build for portability",
				"let later refinement restore gomod prefetch only when the repo shape and toolchain are verified",
			},
		})
	}

	if f.HasMakefile && strings.Contains(a.SelectedStrategy, "make") {
		out = append(out, UnresolvedItem{
			Code:     "artifacts.make_output_unknown",
			Message:  "make-driven build output may still be repo-specific even though the baseline uses normalized artifact paths",
			Severity: "medium",
			Suggestions: []string{
				"inspect Makefile targets and CI files to determine repo-specific output artifact paths",
			},
		})
	}

	if len(a.Languages) > 1 {
		out = append(out, UnresolvedItem{
			Code:     "repo.multiple_languages",
			Message:  "multiple language ecosystems were detected; primary package intent may be mixed",
			Severity: "medium",
			Suggestions: []string{
				"prefer CI/build scripts over manifest-first guesses",
				"preserve alternative build styles and components for later refinement",
			},
		})
	}

	if len(a.BuildDrivers) > 2 {
		out = append(out, UnresolvedItem{
			Code:     "build.multiple_drivers",
			Message:  "multiple build drivers were detected; the best baseline strategy may still need refinement",
			Severity: "medium",
			Suggestions: []string{
				"compare Makefile, CI files, and manifest scripts",
			},
		})
	}

	if a.RepoShape.HasMultipleBins {
		out = append(out, UnresolvedItem{
			Code:     "artifacts.multi_bin_selection",
			Message:  "multiple binary entrypoints were detected; baseline preserved alternatives but may still need refinement",
			Severity: "medium",
			Suggestions: []string{
				"add additional binaries",
				"choose the intended package target",
			},
		})
	}

	if f.HasMakefile && strings.Contains(a.SelectedStrategy, "make") {
		out = append(out, UnresolvedItem{
			Code:     "dependencies.build",
			Message:  "make-driven baseline builds often need extra native/system dependencies beyond the obvious toolchain packages",
			Severity: "medium",
			Suggestions: []string{
				"infer extra build deps from Makefile, CI, manifest scripts, and install context",
			},
		})
	}

	if len(a.InstallLayout.Binaries) == 0 && (f.PrimaryType == "go" || f.PrimaryType == "rust") {
		out = append(out, UnresolvedItem{
			Code:     "install_layout.binaries",
			Message:  "no strong install-layout binary evidence was found; baseline used fallback artifact paths",
			Severity: "medium",
			Suggestions: []string{
				"inspect install commands, Makefile targets, and release workflows",
			},
		})
	}

	if a.Alternatives != nil && len(a.Alternatives.Components) > 1 {
		if absInt(a.Alternatives.Components[0].Score-a.Alternatives.Components[1].Score) <= 10 {
			out = append(out, UnresolvedItem{
				Code:     "component.selection_close",
				Message:  "top component candidates are close in score",
				Severity: "medium",
				Suggestions: []string{
					"inspect CLI usage, service units, and install targets",
				},
			})
		}
	}
	if a.Alternatives != nil && len(a.Alternatives.BuildStyles) > 1 {
		if absInt(a.Alternatives.BuildStyles[0].Score-a.Alternatives.BuildStyles[1].Score) <= 10 {
			out = append(out, UnresolvedItem{
				Code:     "build_style.selection_close",
				Message:  "top build-style candidates are close in score",
				Severity: "medium",
				Suggestions: []string{
					"inspect CI, Makefile, and release scripts to choose the intended baseline build path",
				},
			})
		}
	}

	if looksTodo(a.Metadata.Description) {
		out = append(out, UnresolvedItem{
			Code:     "description",
			Message:  "description is missing or placeholder",
			Severity: "low",
		})
	}
	if looksTodo(a.Metadata.License) {
		out = append(out, UnresolvedItem{
			Code:     "license",
			Message:  "license is missing or placeholder",
			Severity: "medium",
		})
	}
	if strings.TrimSpace(a.Metadata.Website) == "" {
		out = append(out, UnresolvedItem{
			Code:     "website",
			Message:  "website/homepage is missing",
			Severity: "low",
		})
	}

	return dedupeUnresolved(out)
}

func cargoWorkspace(repoDir string) bool {
	return fileContains(filepath.Join(repoDir, "Cargo.toml"), "[workspace]")
}

func nodeWorkspace(repoDir string) bool {
	return fileContains(filepath.Join(repoDir, "package.json"), `"workspaces"`)
}

func discoverCandidateComponents(f *RepoFacts) []string {
	if f != nil && len(f.Components) > 0 {
		var out []string
		for _, c := range f.Components {
			if s := sanitizeName(c.Name); s != "" && s != "unknown" {
				out = append(out, s)
			}

			if s := canonicalRepoName(c.Name); s != "" && !isLikelyBuildHelperBinary(s) {
				out = append(out, s)
			}
		}
		sort.Strings(out)
		return dedupeStrings(out)
	}

	var out []string
	add := func(v string) {
		v = sanitizeName(strings.TrimSpace(v))
		if v == "" || v == "unknown" {
			return
		}
		for _, x := range out {
			if x == v {
				return
			}
		}
		out = append(out, v)
	}

	add(f.Name)
	add(f.CargoBinName)
	add(f.NodeBinName)
	add(f.PythonConsoleScript)

	for _, rel := range f.GoMainCandidates {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "." || rel == "" {
			add(f.Name)
			continue
		}
		add(filepath.Base(rel))
	}

	sort.Strings(out)
	return out
}

func discoverServices(f *RepoFacts) []ServiceHint {
	if f != nil && len(f.Services) > 0 {
		return append([]ServiceHint(nil), f.Services...)
	}

	var out []ServiceHint
	paths := []string{
		"systemd",
		"packaging/systemd",
		"deploy/systemd",
		"contrib/systemd",
	}
	for _, root := range paths {
		full := filepath.Join(f.RepoDir, root)
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
				Reason:     "systemd service file discovered in repository",
			})
		}
	}
	return dedupeServiceHints(out)
}

func discoverManpages(f *RepoFacts) []string {
	if f != nil && len(f.ManpagePaths) > 0 {
		return append([]string(nil), f.ManpagePaths...)
	}

	var out []string
	roots := []string{"man", "doc/man", "share/man"}
	for _, root := range roots {
		full := filepath.Join(f.RepoDir, root)
		_ = filepath.Walk(full, func(path string, info os.FileInfo, err error) error {
			if err != nil || info == nil || info.IsDir() {
				return nil
			}
			name := info.Name()
			if strings.HasSuffix(name, ".1") || strings.HasSuffix(name, ".5") || strings.HasSuffix(name, ".8") {
				if rel, err := filepath.Rel(f.RepoDir, path); err == nil {
					out = append(out, filepath.ToSlash(rel))
				}
			}
			return nil
		})
	}
	sort.Strings(out)
	return dedupeStrings(out)
}

func discoverConfigPaths(f *RepoFacts) []string {
	if f != nil && len(f.ConfigPaths) > 0 {
		return append([]string(nil), f.ConfigPaths...)
	}

	var out []string
	roots := []string{"etc", "packaging", "dist", "contrib"}

	for _, root := range roots {
		full := filepath.Join(f.RepoDir, root)
		_ = filepath.Walk(full, func(path string, info os.FileInfo, err error) error {
			if err != nil || info == nil || info.IsDir() {
				return nil
			}

			rel, err := filepath.Rel(f.RepoDir, path)
			if err != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)

			if !looksInstallableConfigPath(rel) {
				return nil
			}

			out = append(out, rel)
			return nil
		})
	}

	sort.Strings(out)
	return dedupeStrings(out)
}

func looksInstallableConfigPath(rel string) bool {
	low := strings.ToLower(strings.TrimSpace(filepath.ToSlash(rel)))
	if low == "" {
		return false
	}

	if strings.HasPrefix(low, "config/") ||
		strings.HasPrefix(low, "configs/") ||
		strings.HasPrefix(low, "deploy/") ||
		strings.Contains(low, "/samples/") ||
		strings.Contains(low, "/crd/") ||
		strings.Contains(low, "/grafana/") ||
		strings.Contains(low, "kustomization.yaml") {
		return false
	}

	if strings.HasPrefix(low, "etc/") ||
		strings.Contains(low, "/etc/") ||
		strings.HasPrefix(low, "packaging/") {
		return strings.HasSuffix(low, ".conf") ||
			strings.HasSuffix(low, ".ini") ||
			strings.HasSuffix(low, ".toml") ||
			strings.HasSuffix(low, ".yaml") ||
			strings.HasSuffix(low, ".yml") ||
			strings.HasSuffix(low, ".json")
	}

	return strings.HasSuffix(low, ".conf") ||
		strings.HasSuffix(low, ".ini") ||
		strings.HasSuffix(low, ".toml")
}

func discoverCICommands(f *RepoFacts) []string {
	var out []string

	if f != nil {
		for _, h := range f.BuildHints {
			out = append(out, h.Command)
		}
		for _, h := range f.TestHints {
			out = append(out, h.Command)
		}
		for _, h := range f.DocHints {
			out = append(out, h.Command)
		}
		if len(out) > 0 {
			return dedupeStrings(out)
		}
	}

	candidates := []string{
		".github/workflows",
		"azure-pipelines.yml",
		".gitlab-ci.yml",
	}
	for _, c := range candidates {
		full := filepath.Join(f.RepoDir, c)
		st, err := os.Stat(full)
		if err != nil {
			continue
		}
		if st.IsDir() {
			files, err := os.ReadDir(full)
			if err != nil {
				continue
			}
			for _, file := range files {
				if file.IsDir() {
					continue
				}
				b, err := os.ReadFile(filepath.Join(full, file.Name()))
				if err != nil {
					continue
				}
				out = append(out, extractCommandLikeLines(string(b))...)
			}
			continue
		}
		b, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		out = append(out, extractCommandLikeLines(string(b))...)
	}
	return dedupeStrings(out)
}

func discoverInstallHints(f *RepoFacts) []string {
	if f != nil && len(f.InstallHints2) > 0 {
		return commandHintsToStrings(f.InstallHints2)
	}

	var out []string
	for _, name := range []string{"Makefile", "GNUmakefile", "makefile"} {
		b, err := os.ReadFile(filepath.Join(f.RepoDir, name))
		if err != nil {
			continue
		}
		out = append(out, extractInstallLikeLines(string(b))...)
	}
	return dedupeStrings(out)
}

func deriveInstallLayout(f *RepoFacts, a *Analysis) InstallLayout {
	var layout InstallLayout

	add := func(dst *[]PathEvidence, pe PathEvidence) {
		pe.Path = filepath.ToSlash(strings.TrimSpace(pe.Path))
		pe.Kind = strings.TrimSpace(pe.Kind)
		pe.Source = strings.TrimSpace(pe.Source)
		pe.Confidence = normalizeConfidence(pe.Confidence)
		pe.Reason = strings.TrimSpace(pe.Reason)
		if pe.Path == "" || pe.Kind == "" {
			return
		}
		for _, cur := range *dst {
			if cur.Path == pe.Path && cur.Kind == pe.Kind {
				return
			}
		}
		*dst = append(*dst, pe)
	}

	// Binary candidates inferred from components/runtime.
	componentNames := dedupeStrings(append([]string(nil), a.CandidateComponents...))
	if a.Runtime.Entrypoint != "" && a.Runtime.Entrypoint != "node" && a.Runtime.Entrypoint != "python3" {
		componentNames = dedupeStrings(append(componentNames, sanitizeName(a.Runtime.Entrypoint)))
	}
	for _, c := range a.Components {
		if name := sanitizeName(c.Name); name != "" && name != "unknown" {
			componentNames = dedupeStrings(append(componentNames, name))
		}
	}
	for _, name := range componentNames {
		switch f.PrimaryType {
		case "go":
			add(&layout.Binaries, PathEvidence{
				Path:       nativeBinaryArtifactPath(name),
				Kind:       "binary",
				Source:     "analysis.component",
				Confidence: "medium",
				Reason:     "normalized Go binary artifact path derived from selected/candidate component",
			})
		case "rust":
			add(&layout.Binaries, PathEvidence{
				Path:       "src/target/release/" + name,
				Kind:       "binary",
				Source:     "analysis.component",
				Confidence: "medium",
				Reason:     "normalized Cargo binary artifact path derived from selected/candidate component",
			})
		}
	}

	for _, mp := range a.ManpagePaths {
		add(&layout.Manpages, PathEvidence{
			Path:       mp,
			Kind:       "manpage",
			Source:     "analysis.manpage",
			Confidence: "medium",
			Reason:     "manpage discovered in repository",
		})
	}
	for _, cp := range a.ConfigPaths {
		add(&layout.ConfigFiles, PathEvidence{
			Path:       cp,
			Kind:       "config",
			Source:     "analysis.config",
			Confidence: "medium",
			Reason:     "config-like file discovered in repository",
		})
	}
	for _, s := range a.Services {
		if strings.TrimSpace(s.UnitPath) != "" {
			add(&layout.Systemd, PathEvidence{
				Path:       s.UnitPath,
				Kind:       "systemd",
				Source:     "analysis.service",
				Confidence: normalizeConfidence(s.Confidence),
				Reason:     defaultString(s.Reason, "service unit discovered in repository"),
			})
		}
		if strings.TrimSpace(s.ConfigPath) != "" {
			add(&layout.ConfigFiles, PathEvidence{
				Path:       s.ConfigPath,
				Kind:       "config",
				Source:     "analysis.service",
				Confidence: normalizeConfidence(s.Confidence),
				Reason:     "service-associated config path discovered in repository",
			})
		}
	}

	// Only add README as a doc artifact for package types that install docs
	if a.SelectedStrategy != "generic-placeholder" && !looksContainerOnlyAnalysis(a) {
                for _, doc := range []string{"README.md", "README"} {
                        full := filepath.Join(f.RepoDir, doc)
                        if fileExists(full) {
                                add(&layout.Docs, PathEvidence{
                                        Path:       filepath.ToSlash(doc),
                                        Kind:       "doc",
                                        Source:     "analysis.doc",
                                        Confidence: "medium",
                                        Reason:     "documentation path discovered in repository",
                                })
                                break
                        }
                }
        }

	for _, dir := range []string{"data", "assets", "share"} {
		if hasDir(filepath.Join(f.RepoDir, dir)) {
			add(&layout.DataDirs, PathEvidence{
				Path:       filepath.ToSlash(dir),
				Kind:       "data_dir",
				Source:     "analysis.data",
				Confidence: "low",
				Reason:     "data-like directory discovered in repository",
			})
		}
	}

	return layout
}

func scoreComponentChoices(f *RepoFacts, a *Analysis) []ScoredChoice {
	type acc struct {
		score    int
		evidence []string
		reason   string
	}

	m := map[string]*acc{}
	add := func(name string, score int, evidence, reason string) {
		name = sanitizeName(name)
		if name == "" || name == "unknown" {
			return
		}
		cur, ok := m[name]
		if !ok {
			cur = &acc{}
			m[name] = cur
		}
		cur.score += score
		if strings.TrimSpace(evidence) != "" {
			cur.evidence = append(cur.evidence, evidence)
		}
		if cur.reason == "" && strings.TrimSpace(reason) != "" {
			cur.reason = reason
		}
	}

	add(f.Name, 20, "repo name", "repository name is a candidate package/component name")
	for _, name := range a.CandidateComponents {
		add(name, 35, "candidate component", "component candidate discovered from repo structure")
	}
	for _, c := range a.Components {
		score := 35
		if c.Role == "cli" {
			score = 50
		}
		if c.Role == "daemon" {
			score = 55
		}
		add(c.Name, score, defaultString(c.Path, "component hint"), defaultString(c.Reason, "structured component hint"))
	}
	for _, s := range a.Services {
		add(s.Name, 60, defaultString(s.UnitPath, "service unit"), defaultString(s.Reason, "service unit strongly suggests runtime component"))
	}
	for _, mt := range a.MakeTargets {
		name := sanitizeName(mt.Name)
		if name == "" || name == "install" || name == "build" || name == "all" || name == "test" {
			continue
		}
		add(name, 15, "make target "+mt.Name, defaultString(mt.Reason, "named Make target may map to a component"))
	}
	for _, cmd := range append(append([]CommandHint(nil), a.BuildHints...), a.InstallHints2...) {
		low := strings.ToLower(cmd.Command)
		for _, name := range a.CandidateComponents {
			if strings.Contains(low, name) {
				add(name, 20, defaultString(cmd.Source, "build/install command"), "component name appears in build/install command")
			}
		}
	}

	if preferred := preferredGoPrimaryComponent(f, a); preferred != "" {
	add(preferred, 40, "primary Go binary heuristic", "multi-binary Go repo primary-binary heuristic")
}
for _, c := range a.CandidateComponents {
	if isAuxiliaryGoBinary(c) {
		add(c, -20, "auxiliary binary heuristic", "likely helper or secondary binary for package baselines")
	}
}

	var out []ScoredChoice
	for name, cur := range m {
		out = append(out, ScoredChoice{
			Value:      name,
			Score:      cur.score,
			Confidence: inferConfidenceFromScore(cur.score),
			Reason:     cur.reason,
			Evidence:   dedupeStrings(cur.evidence),
		})
	}
	sortScoredChoices(out)
	return out
}

func scoreBuildStyleChoices(f *RepoFacts, a *Analysis) []ScoredChoice {
	type acc struct {
		score    int
		evidence []string
		reason   string
	}

	m := map[string]*acc{}
	add := func(name string, score int, evidence, reason string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		cur, ok := m[name]
		if !ok {
			cur = &acc{}
			m[name] = cur
		}
		cur.score += score
		if strings.TrimSpace(evidence) != "" {
			cur.evidence = append(cur.evidence, evidence)
		}
		if cur.reason == "" && strings.TrimSpace(reason) != "" {
			cur.reason = reason
		}
	}

	switch f.PrimaryType {
	case "go":
		if f.HasMakefile {
			add("go-make", 60, "Makefile", "Go repo with Makefile strongly suggests make-driven build")
			if len(f.GoMainCandidates) > 1 || a.RepoShape.HasMultipleBins {
				add("go-make-multi-bin", 70, "Makefile + multi-bin", "Make-driven Go repository with multiple binaries")
			}
		}
		if len(f.GoMainCandidates) > 1 || a.RepoShape.HasMultipleBins {
			add("go-multi-bin", 55, "multiple Go main packages", "multiple Go main packages discovered")
		}
		add("go-simple", 45, "go.mod", "single-module Go repository")
	case "rust":
		if cargoWorkspace(f.RepoDir) || a.RepoShape.HasWorkspace {
			add("rust-workspace", 65, "Cargo workspace", "workspace Cargo repository")
		}
		add("rust-simple", 45, "Cargo.toml", "Cargo repository")
	case "node":
		switch f.NodePackageManager {
		case "pnpm":
			add("node-pnpm-app", 60, "pnpm lock/metadata", "Node repository uses pnpm")
		case "yarn":
			add("node-yarn-app", 60, "yarn lock/metadata", "Node repository uses yarn")
		default:
			add("node-npm-app", 60, "package-lock/npm metadata", "Node repository uses npm")
		}
	case "python":
		if f.HasPyProject {
			add("python-wheel", 60, "pyproject.toml", "pyproject-based Python package")
		}
		if f.HasRequirements || f.HasSetupPy {
			add("python-requirements", 45, "requirements/setup.py", "requirements/setup.py-based Python packaging")
		}
	default:
		add("generic-placeholder", 10, "unknown repo type", "no strong language-specific baseline build style")
	}

	for _, h := range a.BuildHints {
		low := strings.ToLower(h.Command)
		switch {
		case strings.Contains(low, "make"):
			add("go-make", 15, defaultString(h.Source, "build hint"), "build hint references make")
		case strings.Contains(low, "cargo build"):
			add("rust-simple", 15, defaultString(h.Source, "build hint"), "build hint references cargo build")
		case strings.Contains(low, "go build"):
			add("go-simple", 15, defaultString(h.Source, "build hint"), "build hint references go build")
		case strings.Contains(low, "pnpm "):
			add("node-pnpm-app", 15, defaultString(h.Source, "build hint"), "build hint references pnpm")
		case strings.Contains(low, "yarn "):
			add("node-yarn-app", 15, defaultString(h.Source, "build hint"), "build hint references yarn")
		case strings.Contains(low, "npm "):
			add("node-npm-app", 15, defaultString(h.Source, "build hint"), "build hint references npm")
		}
	}
	for _, h := range a.DocHints {
		low := strings.ToLower(h.Command)
		if strings.Contains(low, "go-md2man") || strings.Contains(low, "md2man") {
			if f.PrimaryType == "go" && f.HasMakefile {
				add("go-make", 10, defaultString(h.Source, "doc hint"), "doc generation hint reinforces make-driven Go build")
			}
		}
	}

	var out []ScoredChoice
	for name, cur := range m {
		out = append(out, ScoredChoice{
			Value:      name,
			Score:      cur.score,
			Confidence: inferConfidenceFromScore(cur.score),
			Reason:     cur.reason,
			Evidence:   dedupeStrings(cur.evidence),
		})
	}
	sortScoredChoices(out)
	return out
}

func scoreEntrypointChoices(f *RepoFacts, a *Analysis) []ScoredChoice {
	type acc struct {
		score    int
		evidence []string
		reason   string
	}

	m := map[string]*acc{}
	add := func(name string, score int, evidence, reason string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		cur, ok := m[name]
		if !ok {
			cur = &acc{}
			m[name] = cur
		}
		cur.score += score
		if evidence != "" {
			cur.evidence = append(cur.evidence, evidence)
		}
		if cur.reason == "" && reason != "" {
			cur.reason = reason
		}
	}

	for _, s := range a.Services {
		add(s.Name, 60, defaultString(s.UnitPath, "service unit"), "service name is a strong daemon entrypoint hint")
	}
	for _, c := range a.Components {
		score := 30
		if c.Role == "daemon" {
			score = 55
		} else if c.Role == "cli" {
			score = 45
		}
		add(c.Name, score, defaultString(c.Path, "component hint"), defaultString(c.Reason, "component hint suggests entrypoint"))
	}
	switch f.PrimaryType {
	case "node":
		if f.NodeBinName != "" {
			add(f.NodeBinName, 40, "package.json bin", "package.json bin suggests CLI entrypoint")
		}
		if f.NodeMain != "" {
			add("node", 30, f.NodeMain, "Node main script suggests node entrypoint")
		}
	case "python":
		if f.PythonConsoleScript != "" {
			add(f.PythonConsoleScript, 55, "console_scripts", "console script strongly suggests CLI entrypoint")
		}
		if f.PythonModuleName != "" {
			add("python3", 35, f.PythonModuleName, "Python module suggests python -m entrypoint")
		}
	default:
		for _, cc := range a.CandidateComponents {
			add(cc, 25, "candidate component", "candidate component can serve as runtime entrypoint")
		}
	}

	var out []ScoredChoice
	for name, cur := range m {
		out = append(out, ScoredChoice{
			Value:      sanitizeName(name),
			Score:      cur.score,
			Confidence: inferConfidenceFromScore(cur.score),
			Reason:     cur.reason,
			Evidence:   dedupeStrings(cur.evidence),
		})
	}
	sortScoredChoices(out)
	return out
}

func scorePackageNameChoices(f *RepoFacts, a *Analysis) []ScoredChoice {
	type acc struct {
		score    int
		evidence []string
		reason   string
	}

	m := map[string]*acc{}
	add := func(name string, score int, evidence, reason string) {
		name = sanitizeName(name)
		if name == "" || name == "unknown" {
			return
		}
		cur, ok := m[name]
		if !ok {
			cur = &acc{}
			m[name] = cur
		}
		cur.score += score
		if evidence != "" {
			cur.evidence = append(cur.evidence, evidence)
		}
		if cur.reason == "" && reason != "" {
			cur.reason = reason
		}
	}

	add(f.Name, 60, "repo metadata", "repository name is the default package name candidate")
	for _, c := range a.CandidateComponents {
		add(c, 25, "candidate component", "component name may be the intended package name")
	}
	if a.Runtime.Entrypoint != "" && a.Runtime.Entrypoint != "node" && a.Runtime.Entrypoint != "python3" {
		add(a.Runtime.Entrypoint, 35, "runtime entrypoint", "runtime entrypoint is a strong package name candidate")
	}

	var out []ScoredChoice
	for name, cur := range m {
		out = append(out, ScoredChoice{
			Value:      name,
			Score:      cur.score,
			Confidence: inferConfidenceFromScore(cur.score),
			Reason:     cur.reason,
			Evidence:   dedupeStrings(cur.evidence),
		})
	}
	sortScoredChoices(out)
	return out
}

func deriveDecisionRecords(f *RepoFacts, a *Analysis) []DecisionRecord {
	var out []DecisionRecord

	if a.Alternatives != nil && len(a.Alternatives.Components) > 0 {
		top := a.Alternatives.Components[0]
		out = append(out, DecisionRecord{
			Field:      "main_component",
			Chosen:     top.Value,
			Confidence: top.Confidence,
			Reason:     top.Reason,
			Evidence:   append([]string(nil), top.Evidence...),
		})
	}
	if a.Alternatives != nil && len(a.Alternatives.BuildStyles) > 0 {
		top := a.Alternatives.BuildStyles[0]
		out = append(out, DecisionRecord{
			Field:      "build_style",
			Chosen:     top.Value,
			Confidence: top.Confidence,
			Reason:     top.Reason,
			Evidence:   append([]string(nil), top.Evidence...),
		})
	}
	if a.Runtime.Entrypoint != "" {
		out = append(out, DecisionRecord{
			Field:      "entrypoint",
			Chosen:     a.Runtime.Entrypoint,
			Confidence: normalizeConfidence(a.Runtime.Confidence),
			Reason:     "runtime inference selected this entrypoint from service/component/language hints",
			Evidence:   bestAlternativeEvidence(a.Alternatives, "entrypoint"),
		})
	}
	if a.Alternatives != nil && len(a.Alternatives.PackageNames) > 0 {
		top := a.Alternatives.PackageNames[0]
		out = append(out, DecisionRecord{
			Field:      "package_name",
			Chosen:     top.Value,
			Confidence: top.Confidence,
			Reason:     top.Reason,
			Evidence:   append([]string(nil), top.Evidence...),
		})
	}

	return dedupeDecisionRecords(out)
}

func extractCommandLikeLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" {
			continue
		}
		low := strings.ToLower(trim)
		switch {
		case strings.HasPrefix(low, "go build"),
			strings.Contains(low, " go build "),
			strings.HasPrefix(low, "go test"),
			strings.HasPrefix(low, "cargo build"),
			strings.HasPrefix(low, "cargo test"),
			strings.HasPrefix(low, "npm "),
			strings.HasPrefix(low, "pnpm "),
			strings.HasPrefix(low, "yarn "),
			strings.HasPrefix(low, "python "),
			strings.HasPrefix(low, "python3 "),
			strings.HasPrefix(low, "pip "),
			strings.HasPrefix(low, "make"),
			strings.Contains(low, " make "):
			out = append(out, trim)
		}
	}
	return dedupeStrings(out)
}

func extractInstallLikeLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" {
			continue
		}
		if strings.Contains(trim, "install ") ||
			strings.Contains(trim, "cp ") ||
			strings.Contains(trim, "mv ") ||
			strings.Contains(trim, "mkdir -p") ||
			strings.Contains(trim, "-o ") {
			out = append(out, trim)
		}
	}
	return dedupeStrings(out)
}

func dedupeStrings(in []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func commandHintsToStrings(in []CommandHint) []string {
	var out []string
	for _, h := range in {
		if s := strings.TrimSpace(h.Command); s != "" {
			out = append(out, s)
		}
	}
	return dedupeStrings(out)
}

func dedupeArtifactHints(in []ArtifactHint) []ArtifactHint {
	seen := map[string]struct{}{}
	var out []ArtifactHint
	for _, a := range in {
		key := strings.TrimSpace(a.Kind) + "|" + strings.TrimSpace(a.Path) + "|" + strings.TrimSpace(a.Name)
		if key == "||" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, a)
	}
	return out
}

func dedupeDecisionRecords(in []DecisionRecord) []DecisionRecord {
	seen := map[string]struct{}{}
	var out []DecisionRecord
	for _, d := range in {
		key := strings.TrimSpace(d.Field) + "|" + strings.TrimSpace(d.Chosen)
		if key == "|" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, d)
	}
	return out
}

func normalizeConfidence(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "high":
		return "high"
	case "medium":
		return "medium"
	case "low":
		return "low"
	default:
		return "low"
	}
}

func inferConfidenceFromScore(score int) string {
	switch {
	case score >= 70:
		return "high"
	case score >= 35:
		return "medium"
	default:
		return "low"
	}
}

func sortScoredChoices(in []ScoredChoice) {
	sort.Slice(in, func(i, j int) bool {
		if in[i].Score == in[j].Score {
			return in[i].Value < in[j].Value
		}
		return in[i].Score > in[j].Score
	})
}

func bestAlternativeEvidence(a *Alternatives, kind string) []string {
	if a == nil {
		return nil
	}
	switch kind {
	case "entrypoint":
		if len(a.EntryPoints) > 0 {
			return append([]string(nil), a.EntryPoints[0].Evidence...)
		}
	}
	return nil
}

func hasArtifactKind(in []ArtifactHint, kind string) bool {
	for _, a := range in {
		if strings.TrimSpace(a.Kind) == kind {
			return true
		}
	}
	return false
}

func defaultString(v, fallback string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return fallback
	}
	return v
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
