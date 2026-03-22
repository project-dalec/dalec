package specgen

import (
	"path/filepath"
	"sort"
	"strings"
)

func nativeBinaryBuildRelPath(name string) string {
	name = sanitizeName(name)
	if name == "" || name == "unknown" {
		name = "app"
	}
	return "bin/" + name
}

func nativeBinaryArtifactPath(name string) string {
	return "src/" + nativeBinaryBuildRelPath(name)
}

func deterministicPlan(opts Options, facts *RepoFacts, analysis *Analysis) *SpecPlan {
	plan := &SpecPlan{
		SchemaVersion: 1,
		Intent:        opts.Intent,
		TargetFamily:  opts.TargetFamily,
		UseTargets:    opts.EmitTargets,
		GenerateTests: false,
		Args:          defaultPlanArgs(opts, facts, analysis),
	}

	if plan.Intent == "" || plan.Intent == IntentAuto {
		plan.Intent = chooseIntent(facts, analysis)
	}
	if plan.TargetFamily == "" || plan.TargetFamily == TargetFamilyAuto {
		plan.TargetFamily = chooseTargetFamily(plan.Intent)
	}

	if analysis != nil {
		plan.Decisions = append(plan.Decisions, analysis.Decisions...)
		plan.Alternatives = analysis.Alternatives
	}

	plan.MainComponent = chooseDefaultComponent(facts, analysis, opts.MainComponent)
	plan.PackageName = chooseDeterministicPackageName(facts, analysis, plan.MainComponent)
	plan.Description = firstNonEmpty(metadataDescription(facts, analysis), "TODO: describe this package")
	plan.License = firstNonEmpty(metadataLicense(facts, analysis), "TODO")
	plan.Website = metadataWebsite(facts, analysis)
	plan.BuildStyle = chooseBuildStyle(facts, analysis)
	plan.Entrypoint, plan.Cmd = chooseRuntimeDefaults(facts, analysis, plan.MainComponent)
	plan.Routes = defaultRoutesForPlan(plan, facts, analysis)
	plan.Artifacts = deterministicArtifacts(facts, analysis, plan)
	plan.Dependencies = deterministicDependencies(facts, analysis, plan)
	plan.GenerateTests = defaultGenerateTests(opts.TestMode, facts, analysis, plan)
	if plan.GenerateTests {
		plan.Tests = deterministicTests(facts, analysis, plan)
	}

	plan.Decisions = append(plan.Decisions,
		DecisionRecord{
			Field:      "intent",
			Chosen:     string(plan.Intent),
			Confidence: confidenceForIntent(plan.Intent, facts, analysis),
			Reason:     "deterministic baseline selected package intent from repo signals",
		},
		DecisionRecord{
			Field:      "target_family",
			Chosen:     string(plan.TargetFamily),
			Confidence: confidenceForTargetFamily(plan.TargetFamily),
			Reason:     "deterministic baseline selected target family from package intent",
		},
		DecisionRecord{
			Field:      "main_component",
			Chosen:     plan.MainComponent,
			Confidence: confidenceFromAlternatives(plan.Alternatives, "components"),
			Reason:     "deterministic baseline selected the top-ranked component candidate",
			Evidence:   bestAlternativeEvidenceForKind(plan.Alternatives, "components"),
		},
		DecisionRecord{
			Field:      "build_style",
			Chosen:     plan.BuildStyle,
			Confidence: confidenceFromAlternatives(plan.Alternatives, "build_styles"),
			Reason:     "deterministic baseline selected the top-ranked build style",
			Evidence:   bestAlternativeEvidenceForKind(plan.Alternatives, "build_styles"),
		},
		DecisionRecord{
			Field:      "package_name",
			Chosen:     plan.PackageName,
			Confidence: confidenceFromAlternatives(plan.Alternatives, "package_names"),
			Reason:     "deterministic baseline selected the best package name candidate",
			Evidence:   bestAlternativeEvidenceForKind(plan.Alternatives, "package_names"),
		},
	)

	if len(plan.Routes) == 0 {
		plan.Unresolved = append(plan.Unresolved, UnresolvedItem{
			Code:     "plan.routes.empty",
			Message:  "no target routes were selected",
			Severity: "high",
		})
	}

	if plan.Intent == IntentContainerOnly && plan.BuildStyle != "container-assembly" {
		plan.Warnings = append(plan.Warnings, "container-only intent selected without a dedicated container assembly build style")
	}

	if plan.Alternatives != nil && len(plan.Alternatives.Components) > 1 {
		if absInt(plan.Alternatives.Components[0].Score-plan.Alternatives.Components[1].Score) <= 10 {
			plan.Unresolved = append(plan.Unresolved, UnresolvedItem{
				Code:     "component.selection_close",
				Message:  "top component candidates are close in score",
				Severity: "medium",
				Suggestions: []string{
					"inspect CLI usage, service units, and install targets",
				},
			})
		}
	}

	if plan.Alternatives != nil && len(plan.Alternatives.BuildStyles) > 1 {
		if absInt(plan.Alternatives.BuildStyles[0].Score-plan.Alternatives.BuildStyles[1].Score) <= 10 {
			plan.Unresolved = append(plan.Unresolved, UnresolvedItem{
				Code:     "build_style.selection_close",
				Message:  "top build style candidates are close in score",
				Severity: "medium",
				Suggestions: []string{
					"inspect CI, Makefile, and release scripts to choose the intended baseline build path",
				},
			})
		}
	}

	plan.Unresolved = dedupeUnresolved(plan.Unresolved)

	plan.Warnings = dedupeWarnings(plan.Warnings)
	plan.Decisions = dedupeDecisionRecords(plan.Decisions)

	return plan
}

func defaultPlanArgs(opts Options, f *RepoFacts, a *Analysis) map[string]string {
	if !opts.EmitArgs {
		return nil
	}

	args := map[string]string{
		"REVISION": "1",
	}

	version := ""
	if a != nil {
		version = strings.TrimSpace(a.Metadata.Version)
	}
	if version == "" && f != nil {
		version = strings.TrimSpace(f.Version)
	}
	if version == "" || looksTodo(version) {
		args["VERSION"] = ""
	} else {
		args["VERSION"] = version
	}

	return args
}

func chooseIntent(f *RepoFacts, a *Analysis) IntentMode {
	if f == nil {
		return IntentPackage
	}

	if looksWindowsCrossRepo(f, a) {
		return IntentWindowsCross
	}
	if looksSysextRepo(f, a) {
		return IntentSysext
	}
	if looksContainerAssemblyRepo(f, a) {
		return IntentContainerOnly
	}

	switch f.PrimaryType {
	case "go", "rust":
		if hasLikelyRuntime(f, a) {
			return IntentPackageContainer
		}
		return IntentPackage
	case "python", "node":
		if hasLikelyRuntime(f, a) {
			return IntentPackageContainer
		}
		return IntentPackage
	default:
		if f.HasDockerfile || f.HasContainerfile {
			return IntentPackageContainer
		}
		return IntentPackage
	}
}

func chooseTargetFamily(intent IntentMode) TargetFamily {
	switch intent {
	case IntentWindowsCross:
		return TargetFamilyWindows
	case IntentContainerOnly:
		return TargetFamilyBoth
	case IntentSysext:
		return TargetFamilyBoth
	default:
		return TargetFamilyBoth
	}
}

func looksWindowsCrossRepo(f *RepoFacts, a *Analysis) bool {
	if f == nil {
		return false
	}

	parts := []string{f.Name, f.Description, f.Website}
	if a != nil {
		parts = append(parts, a.PackageHints...)
		for _, h := range a.BuildHints {
			parts = append(parts, h.Command)
		}
		for _, h := range a.InstallHints2 {
			parts = append(parts, h.Command)
		}
	}
	low := strings.ToLower(strings.Join(parts, " "))

	if strings.Contains(low, "windowscross") {
		return true
	}
	if strings.Contains(low, "goos=windows") || strings.Contains(low, "targetos=windows") {
		return true
	}
	if strings.Contains(low, ".exe") && strings.Contains(low, "windows") {
		return true
	}
	if strings.Contains(low, "cross compile") && strings.Contains(low, "windows") {
		return true
	}

	return false
}

func looksContainerAssemblyRepo(f *RepoFacts, a *Analysis) bool {
	if f == nil {
		return false
	}

	hasLanguageProject := f.HasGoMod || f.HasCargoToml || f.HasPackageJSON || f.HasPyProject || f.HasRequirements || f.HasSetupPy
	if (f.HasDockerfile || f.HasContainerfile) && !hasLanguageProject {
		return true
	}

	if a != nil {
		if a.SelectedStrategy == "container-assembly" {
			return true
		}
		if a.SelectedStrategy == "generic-placeholder" && (f.HasDockerfile || f.HasContainerfile) && !hasLikelyRuntime(f, a) {
			return true
		}
	}

	return false
}

func looksSysextRepo(f *RepoFacts, a *Analysis) bool {
	if f == nil && a == nil {
		return false
	}

	var haystack []string
	if f != nil {
		haystack = append(haystack, f.Name, f.Description)
	}
	if a != nil {
		haystack = append(haystack, a.InstallHints...)
		a.ConfigPaths = dedupeStrings(a.ConfigPaths)
		haystack = append(haystack, a.ConfigPaths...)
		for _, h := range a.BuildHints {
			haystack = append(haystack, h.Command)
		}
		for _, h := range a.InstallHints2 {
			haystack = append(haystack, h.Command)
		}
	}
	low := strings.ToLower(strings.Join(haystack, " "))

	return strings.Contains(low, "sysext") ||
		strings.Contains(low, "extension-release") ||
		strings.Contains(low, "system extension")
}

func hasLikelyRuntime(f *RepoFacts, a *Analysis) bool {
	if a != nil {
		if len(a.Services) > 0 {
			return true
		}
		if strings.TrimSpace(a.Runtime.Entrypoint) != "" {
			return true
		}
		if len(a.CandidateComponents) > 0 {
			return true
		}
	}
	if f == nil {
		return false
	}
	switch {
	case len(f.GoMainCandidates) > 0:
		return true
	case strings.TrimSpace(f.CargoBinName) != "":
		return true
	case strings.TrimSpace(f.NodeBinName) != "":
		return true
	case strings.TrimSpace(f.NodeMain) != "":
		return true
	case strings.TrimSpace(f.PythonConsoleScript) != "":
		return true
	case strings.TrimSpace(f.PythonModuleName) != "":
		return true
	default:
		return false
	}
}

func chooseDefaultComponent(f *RepoFacts, a *Analysis, explicit string) string {
	if s := sanitizeName(explicit); s != "" && s != "unknown" {
		return s
	}

	if a != nil && a.Alternatives != nil && len(a.Alternatives.Components) > 0 {
		if s := sanitizeName(a.Alternatives.Components[0].Value); s != "" && s != "unknown" {
			return s
		}
	}

	if f != nil {
		if len(f.GoMainCandidates) == 1 {
			if rel := strings.TrimSpace(f.GoMainCandidates[0]); rel == "." || rel == "" {
				if s := sanitizeName(f.Name); s != "" && s != "unknown" {
					return s
				}
			} else {
				if s := sanitizeName(filepath.Base(rel)); s != "" && s != "." && s != "unknown" {
					return s
				}
			}
		}

		for _, s := range []string{f.CargoBinName, f.NodeBinName, f.PythonConsoleScript} {
			if s = sanitizeName(s); s != "" && s != "unknown" {
				return s
			}
		}

		if s := sanitizeName(f.Name); s != "" && s != "unknown" {
			return s
		}
	}

	if a != nil {
		for _, c := range a.CandidateComponents {
			if c = sanitizeName(c); c != "" && c != "unknown" {
				return c
			}
		}
	}

	return "app"
}

func chooseDeterministicPackageName(f *RepoFacts, a *Analysis, main string) string {
	if a != nil && a.Alternatives != nil && len(a.Alternatives.PackageNames) > 0 {
		if s := sanitizeName(a.Alternatives.PackageNames[0].Value); s != "" && s != "unknown" {
			return s
		}
	}
	if f != nil {
		if s := sanitizeName(f.Name); s != "" && s != "unknown" && !looksSemanticMajorOnly(s) {
			return s
		}
	}
	if s := sanitizeName(main); s != "" && s != "unknown" {
		return s
	}
	if a != nil && a.Metadata.Name != "" {
		if s := sanitizeName(a.Metadata.Name); s != "" && s != "unknown" {
			return s
		}
	}
	return "unknown"
}

func metadataDescription(f *RepoFacts, a *Analysis) string {
	if a != nil && strings.TrimSpace(a.Metadata.Description) != "" {
		return a.Metadata.Description
	}
	if f != nil {
		return f.Description
	}
	return ""
}

func metadataLicense(f *RepoFacts, a *Analysis) string {
	if a != nil && strings.TrimSpace(a.Metadata.License) != "" {
		return a.Metadata.License
	}
	if f != nil {
		return f.License
	}
	return ""
}

func metadataWebsite(f *RepoFacts, a *Analysis) string {
	if a != nil && strings.TrimSpace(a.Metadata.Website) != "" {
		return a.Metadata.Website
	}
	if f != nil {
		return f.Website
	}
	return ""
}

func chooseBuildStyle(f *RepoFacts, a *Analysis) string {
	if looksContainerAssemblyRepo(f, a) {
		return "container-assembly"
	}
	if a != nil && a.Alternatives != nil && len(a.Alternatives.BuildStyles) > 0 {
		if s := strings.TrimSpace(a.Alternatives.BuildStyles[0].Value); s != "" {
			return s
		}
	}
	if a != nil && strings.TrimSpace(a.SelectedStrategy) != "" {
		return a.SelectedStrategy
	}
	if f == nil {
		return "generic-placeholder"
	}

	switch f.PrimaryType {
	case "go":
		if f.HasMakefile && len(f.GoMainCandidates) > 1 {
			return "go-make-multi-bin"
		}
		if f.HasMakefile {
			return "go-make"
		}
		if len(f.GoMainCandidates) > 1 {
			return "go-multi-bin"
		}
		return "go-simple"
	case "rust":
		if cargoWorkspace(f.RepoDir) {
			return "rust-workspace"
		}
		return "rust-simple"
	case "node":
		switch f.NodePackageManager {
		case "pnpm":
			return "node-pnpm-app"
		case "yarn":
			return "node-yarn-app"
		default:
			return "node-npm-app"
		}
	case "python":
		if f.HasPyProject {
			return "python-wheel"
		}
		return "python-requirements"
	default:
		if f.HasMakefile {
			return "generic-make"
		}
		return "generic-placeholder"
	}
}

func chooseRuntimeDefaults(f *RepoFacts, a *Analysis, main string) (string, string) {
	if a != nil && strings.TrimSpace(a.Runtime.Entrypoint) != "" {
		return a.Runtime.Entrypoint, a.Runtime.Cmd
	}

	if a != nil && a.Alternatives != nil && len(a.Alternatives.EntryPoints) > 0 {
		entry := strings.TrimSpace(a.Alternatives.EntryPoints[0].Value)
		if entry != "" {
			if entry == "node" && f != nil && strings.TrimSpace(f.NodeMain) != "" {
				return "node", f.NodeMain
			}
			if entry == "python3" && f != nil && strings.TrimSpace(f.PythonModuleName) != "" {
				return "python3", "-m " + f.PythonModuleName
			}
			return entry, ""
		}
	}

	if f == nil {
		return sanitizeName(main), ""
	}

	switch f.PrimaryType {
	case "go", "rust":
		return sanitizeName(main), "--help"
	case "node":
		if f.NodeMain != "" {
			return "node", f.NodeMain
		}
		if f.NodeBinName != "" {
			return f.NodeBinName, ""
		}
	case "python":
		if f.PythonConsoleScript != "" {
			return f.PythonConsoleScript, ""
		}
		if f.PythonModuleName != "" {
			return "python3", "-m " + f.PythonModuleName
		}
	}

	return sanitizeName(main), ""
}

func defaultRoutesForPlan(plan *SpecPlan, f *RepoFacts, a *Analysis) []TargetRoute {
	switch plan.TargetFamily {
	case TargetFamilyRPM:
		return []TargetRoute{
			{Name: "azlinux3", Subtarget: "rpm", Confidence: "medium", Reason: "default RPM target family route"},
		}
	case TargetFamilyDEB:
		return []TargetRoute{
			{Name: "jammy", Subtarget: "deb", Confidence: "medium", Reason: "default DEB target family route"},
			{Name: "noble", Subtarget: "deb", Confidence: "low", Reason: "secondary DEB route for baseline portability"},
		}
	case TargetFamilyWindows:
		return []TargetRoute{
			{Name: "windowscross", Subtarget: "container", Confidence: "medium", Reason: "windows cross-compilation route"},
		}
	default:
		routes := []TargetRoute{
			{Name: "azlinux3", Subtarget: "rpm", Confidence: "medium", Reason: "default RPM baseline route"},
			{Name: "jammy", Subtarget: "deb", Confidence: "medium", Reason: "default DEB baseline route"},
		}
		if plan.Intent == IntentPackageContainer || plan.Intent == IntentContainerOnly {
			routes = append(routes, TargetRoute{
				Name:       "azlinux3",
				Subtarget:  "container",
				Confidence: "low",
				Reason:     "container-capable route included for runtime image refinement",
			})
		}
		return dedupeRoutes(routes)
	}
}

func deterministicArtifacts(f *RepoFacts, a *Analysis, plan *SpecPlan) []PlannedArtifact {
	var out []PlannedArtifact

	add := func(kind, path, subpath, name, target, confidence, reason string, required bool) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		out = append(out, PlannedArtifact{
			Kind:       kind,
			Path:       path,
			Subpath:    strings.TrimSpace(subpath),
			Name:       strings.TrimSpace(name),
			Target:     strings.TrimSpace(target),
			Required:   required,
			Confidence: normalizeConfidence(confidence),
			Reason:     strings.TrimSpace(reason),
		})
	}

	main := sanitizeName(plan.MainComponent)
	if main == "" || main == "unknown" {
		main = sanitizeName(plan.PackageName)
	}
	if main == "" || main == "unknown" {
		main = "app"
	}

	if a != nil {
		for _, pe := range a.InstallLayout.Binaries {
			add("binary", pe.Path, "", filepath.Base(pe.Path), "", pe.Confidence, pe.Reason, true)
		}
		for _, pe := range a.InstallLayout.Manpages {
			add("manpage", pe.Path, "", filepath.Base(pe.Path), "", pe.Confidence, pe.Reason, false)
		}
		for _, pe := range a.InstallLayout.ConfigFiles {
			add("config", pe.Path, "", filepath.Base(pe.Path), "", pe.Confidence, pe.Reason, false)
		}
		for _, pe := range a.InstallLayout.Docs {
			add("doc", pe.Path, "", filepath.Base(pe.Path), "", pe.Confidence, pe.Reason, false)
		}
		for _, pe := range a.InstallLayout.DataDirs {
			add("data_dir", pe.Path, "", filepath.Base(pe.Path), "", pe.Confidence, pe.Reason, false)
		}
		for _, pe := range a.InstallLayout.Libexec {
			add("libexec", pe.Path, "", filepath.Base(pe.Path), "", pe.Confidence, pe.Reason, false)
		}
		for _, pe := range a.InstallLayout.Systemd {
			add("systemd", pe.Path, "", filepath.Base(pe.Path), "", pe.Confidence, pe.Reason, false)
		}
	}

	// Fallback artifact shaping when install-layout evidence is sparse.
	if !hasPlannedArtifactKind(out, "binary") && plan.Intent != IntentContainerOnly {
		switch {
		case strings.HasPrefix(plan.BuildStyle, "go"):
			conf := "medium"
			reason := "fallback Go binary artifact path"
			if f != nil && f.HasMakefile {
				conf = "low"
				reason = "make-driven build; baseline normalized binary artifact path conservatively"
			}
			path := nativeBinaryArtifactPath(main)
			if plan.Intent == IntentWindowsCross || plan.TargetFamily == TargetFamilyWindows {
				path += ".exe"
			}
			add("binary", path, "", filepath.Base(path), "", conf, reason, true)
		case strings.HasPrefix(plan.BuildStyle, "rust"):
			add("binary", "src/target/release/"+main, "", main, "", "medium", "fallback Cargo release artifact path", true)
		}
	}
	if !hasPlannedArtifactKind(out, "doc") && f != nil {
		// Only emit doc artifacts for language types that naturally produce them
		switch {
		case strings.HasPrefix(plan.BuildStyle, "python"),
			strings.HasPrefix(plan.BuildStyle, "node"):
			add("doc", "src/README.md", "", "README.md", "", "low", "baseline documentation fallback", false)
		}
	}

	return dedupePlannedArtifacts(out)
}

func deterministicDependencies(f *RepoFacts, a *Analysis, plan *SpecPlan) []PlannedDependency {
	var out []PlannedDependency

	add := func(scope, name, confidence, reason string) {
		name = strings.TrimSpace(name)
		scope = strings.TrimSpace(scope)
		if name == "" || scope == "" {
			return
		}
		out = append(out, PlannedDependency{
			Name:       name,
			Scope:      scope,
			Confidence: normalizeConfidence(confidence),
			Reason:     strings.TrimSpace(reason),
		})
	}

	switch {
	case strings.HasPrefix(plan.BuildStyle, "go"):
		add("build", "golang", "high", "Go repository detected")
		if f != nil && f.HasMakefile {
			add("build", "make", "high", "top-level Makefile detected")
		}
	case strings.HasPrefix(plan.BuildStyle, "rust"):
		add("build", "rust", "high", "Cargo repository detected")
		add("build", "cargo", "high", "Cargo repository detected")
	case strings.HasPrefix(plan.BuildStyle, "node"):
		add("build", "nodejs", "high", "Node package manifest detected")
		switch {
		case strings.HasPrefix(plan.BuildStyle, "node-pnpm"):
			add("build", "pnpm", "medium", "pnpm-based Node baseline")
		case strings.HasPrefix(plan.BuildStyle, "node-yarn"):
			add("build", "yarn", "medium", "yarn-based Node baseline")
		default:
			add("build", "npm", "medium", "npm-based Node baseline")
		}
	case strings.HasPrefix(plan.BuildStyle, "python"):
		add("build", "python3", "high", "Python project detected")
		add("build", "python3-pip", "medium", "Python packaging baseline")
	case strings.HasPrefix(plan.BuildStyle, "generic-make"):
		add("build", "make", "high", "top-level Makefile detected")
	case plan.Intent == IntentContainerOnly:
		// No default build deps; container-only may be pure assembly.
	}

	if a != nil {
		for _, hint := range append(append([]CommandHint(nil), a.BuildHints...), a.InstallHints2...) {
			h := strings.ToLower(hint.Command)
			switch {
			case strings.Contains(h, "pkg-config"):
				add("build", "pkg-config", "medium", "pkg-config usage detected")
			case strings.Contains(h, "gcc") || strings.Contains(h, "clang") || strings.Contains(h, " cc "):
				add("build", "gcc", "low", "native compiler usage detected")
			case strings.Contains(h, "go-md2man") || strings.Contains(h, "md2man"):
				add("build", "go-md2man", "low", "manpage generation hint detected")
			case strings.Contains(h, "tar "):
				add("build", "tar", "low", "archive tooling detected")
			case strings.Contains(h, "gzip") || strings.Contains(h, "gunzip"):
				add("build", "gzip", "low", "compression tooling detected")
			case strings.Contains(h, "rsync "):
				add("build", "rsync", "low", "rsync usage detected")
			}
		}
		
		for _, h := range a.TestHints {
                        low := strings.ToLower(h.Command)
                        switch {
                        case strings.Contains(low, "pytest"):
                                add("test", "python3-pytest", "low", "pytest test hint detected")
                        // golang and cargo are already in build deps — do not duplicate into test
                        }
                }

		for _, _ = range a.ManpagePaths {
			if hasManpageBuildHints(a) {
    				add("build", "go-md2man", "low", "go-md2man usage detected in build hints")
			}
		}
	}

	return dedupePlannedDeps(out)
}

func defaultGenerateTests(mode TestMode, f *RepoFacts, a *Analysis, plan *SpecPlan) bool {
	switch mode {
	case TestAlways:
		return true
	case TestNever:
		return false
	}

	if plan.Intent == IntentContainerOnly {
		return false
	}
	if len(plan.Artifacts) > 0 {
		return true
	}
	if plan.Entrypoint != "" {
		return true
	}
	if f != nil && (f.PrimaryType == "go" || f.PrimaryType == "rust" || f.PythonConsoleScript != "" || f.NodeBinName != "") {
		return true
	}
	if a != nil && len(a.Services) > 0 {
		return true
	}

	return false
}

func deterministicTests(f *RepoFacts, a *Analysis, plan *SpecPlan) []PlannedTest {
	name := sanitizeName(plan.MainComponent)
	if name == "" || name == "unknown" {
		name = "smoke"
	}

	var tests []PlannedTest

	files := map[string]string{}
	for _, art := range plan.Artifacts {
		switch art.Kind {
		case "binary":
			files["/usr/bin/"+artifactInstalledName(art, name)] = "exists"
		case "manpage":
			files["/usr/share/man/"+manpageInstalledSubpath(art.Path)] = "exists"
		case "config":
			files[defaultConfigInstallPath(art.Path)] = "exists"
		case "systemd":
			files["/usr/lib/systemd/system/"+filepath.Base(art.Path)] = "exists"
		}
	}

	if len(files) > 0 {
		tests = append(tests, PlannedTest{
			Name:       name + "-files",
			Files:      files,
			Confidence: "medium",
			Reason:     "file-based validation generated from planned artifacts",
		})
	}

	if plan.Entrypoint != "" {

		// Use the installed binary path if we know it
		installedBin := "/usr/bin/" + sanitizeName(plan.Entrypoint)
		cmd := installedBin
		if strings.TrimSpace(plan.Cmd) != "" {
			cmd += " " + strings.TrimSpace(plan.Cmd)
		}

		conf := "low"
		reason := "runtime smoke test generated from entrypoint"
		if strings.HasPrefix(plan.BuildStyle, "go") || strings.HasPrefix(plan.BuildStyle, "rust") {
			conf = "medium"
			reason = "runtime smoke test generated from compiled CLI-style entrypoint"
		}

		tests = append(tests, PlannedTest{
			Name:       name + "-smoke",
			Steps:      []string{cmd},
			Confidence: conf,
			Reason:     reason,
		})
	}

	return dedupePlannedTests(tests)
}

func artifactInstalledName(art PlannedArtifact, fallback string) string {
	if s := sanitizeName(art.Name); s != "" && s != "unknown" {
		return s
	}
	base := filepath.Base(strings.TrimSpace(art.Path))
	base = strings.TrimSuffix(base, ".exe")
	if s := sanitizeName(base); s != "" && s != "unknown" {
		return s
	}
	return fallback
}

func manpageInstalledSubpath(src string) string {
	base := filepath.Base(strings.TrimSpace(src))
	if base == "" {
		return "man1/app.1"
	}
	section := "1"
	parts := strings.Split(base, ".")
	if len(parts) >= 2 {
		last := parts[len(parts)-1]
		if len(last) == 1 && last[0] >= '1' && last[0] <= '9' {
			section = last
		}
	}
	return "man" + section + "/" + base
}

func defaultConfigInstallPath(src string) string {
	base := filepath.Base(strings.TrimSpace(src))
	if base == "" {
		base = "app.conf"
	}
	return "/etc/" + base
}

func dedupePlannedArtifacts(in []PlannedArtifact) []PlannedArtifact {
	seen := map[string]struct{}{}
	out := make([]PlannedArtifact, 0, len(in))
	for _, item := range in {
		key := item.Kind + "|" + item.Path + "|" + item.Target
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind == out[j].Kind {
			return out[i].Path < out[j].Path
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

func dedupePlannedDeps(in []PlannedDependency) []PlannedDependency {
	seen := map[string]struct{}{}
	out := make([]PlannedDependency, 0, len(in))
	for _, item := range in {
		key := item.Scope + "|" + item.Name + "|" + item.Target
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Scope == out[j].Scope {
			return out[i].Name < out[j].Name
		}
		return out[i].Scope < out[j].Scope
	})
	return out
}

func dedupePlannedTests(in []PlannedTest) []PlannedTest {
	seen := map[string]struct{}{}
	out := make([]PlannedTest, 0, len(in))
	for _, item := range in {
		key := strings.TrimSpace(item.Name) + "|" + strings.Join(item.Steps, ";")
		if len(item.Files) > 0 {
			keys := make([]string, 0, len(item.Files))
			for k := range item.Files {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			key += "|" + strings.Join(keys, ";")
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func hasPlannedArtifactKind(in []PlannedArtifact, kind string) bool {
	for _, a := range in {
		if strings.TrimSpace(a.Kind) == kind {
			return true
		}
	}
	return false
}

func dedupeRoutes(in []TargetRoute) []TargetRoute {
	seen := map[string]struct{}{}
	out := make([]TargetRoute, 0, len(in))
	for _, item := range in {
		key := strings.TrimSpace(item.Name) + "|" + strings.TrimSpace(item.Subtarget)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].Subtarget < out[j].Subtarget
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func confidenceForIntent(intent IntentMode, f *RepoFacts, a *Analysis) string {
	switch intent {
	case IntentWindowsCross, IntentSysext, IntentContainerOnly:
		return "medium"
	case IntentPackageContainer:
		if hasLikelyRuntime(f, a) {
			return "high"
		}
		return "medium"
	default:
		return "medium"
	}
}

func confidenceForTargetFamily(family TargetFamily) string {
	switch family {
	case TargetFamilyWindows:
		return "high"
	case TargetFamilyRPM, TargetFamilyDEB:
		return "high"
	default:
		return "medium"
	}
}

func confidenceFromAlternatives(a *Alternatives, kind string) string {
	if a == nil {
		return "low"
	}

	var choices []ScoredChoice
	switch kind {
	case "components":
		choices = a.Components
	case "build_styles":
		choices = a.BuildStyles
	case "entrypoints":
		choices = a.EntryPoints
	case "package_names":
		choices = a.PackageNames
	default:
		return "low"
	}

	if len(choices) == 0 {
		return "low"
	}
	return normalizeConfidence(choices[0].Confidence)
}

func bestAlternativeEvidenceForKind(a *Alternatives, kind string) []string {
	if a == nil {
		return nil
	}

	switch kind {
	case "components":
		if len(a.Components) > 0 {
			return append([]string(nil), a.Components[0].Evidence...)
		}
	case "build_styles":
		if len(a.BuildStyles) > 0 {
			return append([]string(nil), a.BuildStyles[0].Evidence...)
		}
	case "entrypoints":
		if len(a.EntryPoints) > 0 {
			return append([]string(nil), a.EntryPoints[0].Evidence...)
		}
	case "package_names":
		if len(a.PackageNames) > 0 {
			return append([]string(nil), a.PackageNames[0].Evidence...)
		}
	}

	return nil
}

func containsFold(items []string, needle string) bool {
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(needle)) {
			return true
		}
	}
	return false
}
