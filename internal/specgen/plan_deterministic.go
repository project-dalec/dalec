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
		UserIntent:    buildUserIntentPlan(opts),
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

	plan.MainComponent = chooseDefaultComponent(facts, analysis, requestedMainComponent(opts))
	plan.PackageName = chooseDeterministicPackageName(facts, analysis, plan.MainComponent, strings.TrimSpace(opts.PackageName))
	plan.PrimaryBinaryName = choosePrimaryBinaryName(opts, plan.MainComponent, plan.PackageName)
	plan.PrimaryBuildTarget = choosePrimaryBuildTarget(opts, facts, plan.MainComponent)
	plan.Description = firstNonEmpty(metadataDescription(facts, analysis), "TODO: describe this package")
	plan.License = firstNonEmpty(metadataLicense(facts, analysis), "TODO")
	plan.Website = metadataWebsite(facts, analysis)
	plan.BuildStyle = chooseBuildStyle(facts, analysis, strings.TrimSpace(opts.BuildStyle))
	if plan.Intent == IntentWindowsCross && looksLikeGoMultiPlatformMatrix(analysis) {
		plan.Intent = IntentPackage
		plan.TargetFamily = chooseTargetFamily(plan.Intent)
	}
	plan.NetworkMode = chooseNetworkMode(facts, plan.BuildStyle)

	// Apply explicit user overrides before runtime/routes/artifacts/tests are
	// derived so emitted image/tests follow the requested primary binary.
	applyExplicitPlanOverrides(opts, facts, analysis, plan)

	plan.Entrypoint, plan.Cmd = chooseRuntimeDefaults(facts, analysis, plan.MainComponent)
	applyExplicitRuntimeOverrides(opts, plan)
	plan.Routes = defaultRoutesForPlan(plan, facts, analysis)
	plan.Artifacts = deterministicArtifacts(facts, analysis, plan)

	// If the user explicitly requested binaries, filter down to that set.
	if len(opts.BinaryNames) > 0 {
		plan.Artifacts = filterArtifactsForRequestedBinaries(plan.Artifacts, opts.BinaryNames)
		if len(plan.Artifacts) == 0 && facts != nil && facts.PrimaryType == "go" {
			plan.Artifacts = plannedGoBinaryArtifacts(opts.BinaryNames)
		}
	}

	// Merge user override deps with scored deps.
	userDeps := append([]PlannedDependency(nil), plan.Dependencies...)
	plan.Dependencies = deterministicDependencies(facts, analysis, plan)
	plan.Dependencies = append(plan.Dependencies, userDeps...)
	plan.Dependencies = dedupePlannedDeps(plan.Dependencies)

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
			Confidence: planDecisionConfidence(plan.UserIntent, "main_component", confidenceFromAlternatives(plan.Alternatives, "components")),
			Reason:     planDecisionReason(plan.UserIntent, "main_component", "deterministic baseline selected the top-ranked component candidate"),
			Evidence:   bestAlternativeEvidenceForKind(plan.Alternatives, "components"),
		},
		DecisionRecord{
			Field:      "build_style",
			Chosen:     plan.BuildStyle,
			Confidence: planDecisionConfidence(plan.UserIntent, "build_style", confidenceFromAlternatives(plan.Alternatives, "build_styles")),
			Reason:     planDecisionReason(plan.UserIntent, "build_style", "deterministic baseline selected the top-ranked build style"),
			Evidence:   bestAlternativeEvidenceForKind(plan.Alternatives, "build_styles"),
		},
		DecisionRecord{
			Field:      "package_name",
			Chosen:     plan.PackageName,
			Confidence: planDecisionConfidence(plan.UserIntent, "package_name", confidenceFromAlternatives(plan.Alternatives, "package_names")),
			Reason:     planDecisionReason(plan.UserIntent, "package_name", "deterministic baseline selected the best package name candidate"),
			Evidence:   bestAlternativeEvidenceForKind(plan.Alternatives, "package_names"),
		},
		DecisionRecord{
			Field:      "entrypoint",
			Chosen:     plan.Entrypoint,
			Confidence: planDecisionConfidence(plan.UserIntent, "entrypoint", confidenceFromAlternatives(plan.Alternatives, "entrypoints")),
			Reason:     planDecisionReason(plan.UserIntent, "entrypoint", "deterministic baseline selected runtime entrypoint defaults"),
			Evidence:   bestAlternativeEvidenceForKind(plan.Alternatives, "entrypoints"),
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

	if !planUserSpecified(plan.UserIntent, "main_component") && plan.Alternatives != nil && len(plan.Alternatives.Components) > 1 {
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

	if !planUserSpecified(plan.UserIntent, "build_style") && plan.Alternatives != nil && len(plan.Alternatives.BuildStyles) > 1 {
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
	plan.Decisions = collapsePlanDecisionRecords(plan.Decisions)

	return plan
}

func buildUserIntentPlan(opts Options) *UserIntentPlan {
	intent := &UserIntentPlan{}
	var explicit []string
	if opts.Intent != "" && opts.Intent != IntentAuto {
		intent.RequestedIntent = opts.Intent
		explicit = append(explicit, "intent")
	}
	if opts.TargetFamily != "" && opts.TargetFamily != TargetFamilyAuto {
		intent.RequestedTargetFamily = opts.TargetFamily
		explicit = append(explicit, "target_family")
	}
	intent.RequestedMainComponent = sanitizeName(strings.TrimSpace(opts.MainComponent))
	if intent.RequestedMainComponent != "" {
		explicit = append(explicit, "main_component")
	}
	if len(opts.BinaryNames) > 0 {
		intent.RequestedBinaryNames = append([]string(nil), opts.BinaryNames...)
		explicit = append(explicit, "binary_names")
	}
	intent.RequestedPackageName = sanitizeName(strings.TrimSpace(opts.PackageName))
	if intent.RequestedPackageName != "" {
		explicit = append(explicit, "package_name")
	}
	intent.RequestedBinaryName = sanitizeName(strings.TrimSpace(opts.BinaryName))
	if intent.RequestedBinaryName != "" {
		explicit = append(explicit, "binary_name")
	}
	intent.RequestedBuildStyle = strings.TrimSpace(opts.BuildStyle)
	if intent.RequestedBuildStyle != "" {
		explicit = append(explicit, "build_style")
	}
	intent.RequestedBuildTarget = strings.TrimSpace(opts.BuildTarget)
	if intent.RequestedBuildTarget != "" {
		explicit = append(explicit, "build_target")
	}
	intent.RequestedEntrypoint = strings.TrimSpace(opts.Entrypoint)
	if intent.RequestedEntrypoint != "" {
		explicit = append(explicit, "entrypoint")
	}
	intent.RequestedCmd = strings.TrimSpace(opts.Command)
	if intent.RequestedCmd != "" {
		explicit = append(explicit, "cmd")
	}
	if opts.TestMode != "" && opts.TestMode != TestAuto {
		intent.RequestedTestMode = opts.TestMode
		explicit = append(explicit, "test_mode")
	}
	intent.ExplicitFields = dedupeStrings(explicit)
	if len(intent.ExplicitFields) == 0 {
		return nil
	}
	return intent
}

func planUserSpecified(intent *UserIntentPlan, field string) bool {
	if intent == nil {
		return false
	}
	for _, item := range intent.ExplicitFields {
		if item == strings.TrimSpace(field) {
			return true
		}
	}
	return false
}

func planDecisionReason(intent *UserIntentPlan, field, fallback string) string {
	if planUserSpecified(intent, field) {
		return "user-specified build intent overrode heuristic selection"
	}
	return fallback
}

func planDecisionConfidence(intent *UserIntentPlan, field, fallback string) string {
	if planUserSpecified(intent, field) {
		return "high"
	}
	return fallback
}

func requestedMainComponent(opts Options) string {
	if s := sanitizeName(strings.TrimSpace(opts.MainComponent)); s != "" && s != "unknown" {
		return s
	}
	if len(opts.BinaryNames) > 0 {
		if s := sanitizeName(opts.BinaryNames[0]); s != "" && s != "unknown" {
			return s
		}
	}
	if s := sanitizeName(strings.TrimSpace(opts.BinaryName)); s != "" && s != "unknown" {
		return s
	}
	return ""
}

func firstBinaryName(items []string) string {
	if len(items) == 0 {
		return ""
	}
	return strings.TrimSpace(items[0])
}

func choosePrimaryBinaryName(opts Options, mainComponent, packageName string) string {
	for _, candidate := range []string{opts.BinaryName, firstBinaryName(opts.BinaryNames), mainComponent, packageName} {
		if s := sanitizeName(candidate); s != "" && s != "unknown" {
			return s
		}
	}
	return "app"
}

func choosePrimaryBuildTarget(opts Options, f *RepoFacts, mainComponent string) string {
	if s := strings.TrimSpace(opts.BuildTarget); s != "" {
		return s
	}
	if f == nil || f.PrimaryType != "go" {
		return ""
	}
	return chooseGoBuildTarget(f, sanitizeName(mainComponent))
}

func collapsePlanDecisionRecords(in []DecisionRecord) []DecisionRecord {
	last := map[string]int{}
	for i, d := range in {
		field := strings.TrimSpace(d.Field)
		if field == "" {
			continue
		}
		last[field] = i
	}
	var out []DecisionRecord
	for i, d := range in {
		field := strings.TrimSpace(d.Field)
		if field == "" {
			continue
		}
		if last[field] != i {
			continue
		}
		out = append(out, d)
	}
	return dedupeDecisionRecords(out)
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

func applyExplicitPlanOverrides(opts Options, facts *RepoFacts, analysis *Analysis, plan *SpecPlan) {
	if plan == nil {
		return
	}

	if s := requestedMainComponent(opts); s != "" {
		plan.MainComponent = s
	}
	if s := sanitizeName(strings.TrimSpace(opts.PackageName)); s != "" && s != "unknown" {
		plan.PackageName = s
	}
	plan.PrimaryBinaryName = choosePrimaryBinaryName(opts, plan.MainComponent, plan.PackageName)
	plan.PrimaryBuildTarget = choosePrimaryBuildTarget(opts, facts, plan.MainComponent)
	if s := strings.TrimSpace(opts.BuildStyle); s != "" {
		plan.BuildStyle = s
	}
	if s := strings.TrimSpace(opts.Entrypoint); s != "" {
		plan.Entrypoint = s
	}
	if strings.TrimSpace(opts.Command) != "" {
		plan.Cmd = strings.TrimSpace(opts.Command)
	}
	if strings.TrimSpace(opts.VersionVarPath) != "" {
		plan.LDFlagsVarPath = strings.TrimSpace(opts.VersionVarPath)
	}
	if opts.CGOEnabled != nil {
		plan.CGOEnabled = opts.CGOEnabled
	}

	for _, dep := range opts.ExtraBuildDeps {
		if dep = strings.TrimSpace(dep); dep != "" {
			plan.Dependencies = append(plan.Dependencies, PlannedDependency{
				Name:       dep,
				Scope:      "build",
				Confidence: "high",
				Reason:     "user-supplied extra build dependency",
			})
		}
	}
	for _, dep := range opts.ExtraRuntimeDeps {
		if dep = strings.TrimSpace(dep); dep != "" {
			plan.Dependencies = append(plan.Dependencies, PlannedDependency{
				Name:       dep,
				Scope:      "runtime",
				Confidence: "high",
				Reason:     "user-supplied extra runtime dependency",
			})
		}
	}
}

// chooseNetworkMode returns the recommended build network policy for a given
// build style. Go and Rust builds with vendored/locked dependencies should use
// "none" for reproducibility. Make-driven and node builds may need outbound
// access and get an empty string (which means the dalec default — sandbox).
func applyExplicitRuntimeOverrides(opts Options, plan *SpecPlan) {
	if plan == nil {
		return
	}
	if s := strings.TrimSpace(opts.Entrypoint); s != "" {
		plan.Entrypoint = s
	}
	if strings.TrimSpace(opts.Command) != "" {
		plan.Cmd = strings.TrimSpace(opts.Command)
	}
}

func looksLikeGoMultiPlatformMatrix(a *Analysis) bool {
	if a == nil {
		return false
	}
	low := []string{}
	for _, h := range a.BuildHints {
		low = append(low, strings.ToLower(strings.TrimSpace(h.Command)))
	}
	joined := strings.Join(low, " ")
	if !strings.Contains(joined, "goos=windows") {
		return false
	}
	otherOS := 0
	for _, osName := range []string{"goos=linux", "goos=darwin", "goos=freebsd", "goos=android", "goos=openbsd", "goos=netbsd"} {
		if strings.Contains(joined, osName) {
			otherOS++
		}
	}
	return otherOS > 0
}

func chooseNetworkMode(f *RepoFacts, buildStyle string) string {
	switch {
	case buildStyle == "go-simple",
		buildStyle == "go-multi-bin":
		return "none"
	case buildStyle == "rust-simple",
		buildStyle == "rust-workspace":
		return "none"
	default:
		// go-make and go-make-multi-bin may invoke arbitrary make targets that
		// pull tooling — leave network open and let the user tighten it later.
		return ""
	}
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
	if prefersPackageContainerIntent(f, a) {
		return IntentPackageContainer
	}
	return IntentPackage
}

func prefersPackageContainerIntent(f *RepoFacts, a *Analysis) bool {
	if f == nil || a == nil {
		return false
	}
	if len(a.Services) > 0 {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(a.Runtime.Kind), "daemon") {
		return true
	}
	for _, c := range a.Components {
		if strings.EqualFold(strings.TrimSpace(c.Role), "daemon") {
			return true
		}
	}
	for _, art := range a.Artifacts {
		if strings.EqualFold(strings.TrimSpace(art.Kind), "systemd") {
			return true
		}
	}
	for _, pe := range a.InstallLayout.Systemd {
		if strings.TrimSpace(pe.Path) != "" {
			return true
		}
	}
	return false
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

	if strings.Contains(low, "windowscross") || strings.Contains(low, "windows cross") {
		return true
	}

	if f.HasGoMod {
		if !containsAnyFold(low, []string{
			"goos=windows",
			"set goos=windows",
			"export goos=windows",
			"targetos=windows",
		}) {
			return false
		}
		if containsAnyFold(low, []string{
			"goos=linux",
			"goos=darwin",
			"goos=freebsd",
			"goos=android",
			"goos=solaris",
			"goos=netbsd",
			"goos=openbsd",
		}) {
			return false
		}
		return true
	}

	if containsAnyFold(low, []string{
		"cargo xwin",
		"cargo-xwin",
		"llvm-mingw",
		"mingw-w64",
		"windres",
		"signtool",
	}) && strings.Contains(low, "windows") {
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
	if a != nil && len(a.Services) > 0 {
		return true
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
	case strings.TrimSpace(f.PythonModuleName) != "" && (f.HasPyProject || f.HasSetupPy):
		return true
	default:
		return false
	}
}

func chooseDefaultComponent(f *RepoFacts, a *Analysis, explicit string) string {
	if s := sanitizeName(explicit); s != "" && s != "unknown" {
		return s
	}

	if s := preferredGoPrimaryComponent(f, a); s != "" {
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

func preferredGoPrimaryComponent(f *RepoFacts, a *Analysis) string {
	if f == nil || f.PrimaryType != "go" || len(f.GoMainCandidates) == 0 {
		return ""
	}

	byName := map[string]string{}
	for _, rel := range f.GoMainCandidates {
		rel = filepath.ToSlash(strings.TrimSpace(rel))

		name := ""
		if rel == "." || rel == "" {
			name = sanitizeName(f.Name)
		} else {
			name = sanitizeName(filepath.Base(rel))
		}
		if name != "" && name != "unknown" {
			byName[name] = rel
		}
	}

	if repo := sanitizeName(f.Name); repo != "" {
		if _, ok := byName[repo]; ok {
			return repo
		}
	}

	if a != nil {
		if s := sanitizeName(a.Runtime.Entrypoint); s != "" && s != "unknown" {
			if _, ok := byName[s]; ok && !isAuxiliaryGoBinary(s) {
				return s
			}
		}
	}

	for _, s := range []string{"operator", "controller", "manager", "server", "daemon", "agent", "apiserver", "api"} {
		if _, ok := byName[s]; ok {
			return s
		}
	}

	if rel := strings.TrimSpace(f.GoMainRel); rel != "" && rel != "." {
		if s := sanitizeName(filepath.Base(rel)); s != "" && s != "unknown" && !isAuxiliaryGoBinary(s) {
			return s
		}
	}

	for _, rel := range f.GoMainCandidates {
		name := sanitizeName(filepath.Base(rel))
		if name != "" && name != "unknown" && !isAuxiliaryGoBinary(name) {
			return name
		}
	}

	return ""
}

func isAuxiliaryGoBinary(name string) bool {
	switch sanitizeName(name) {
	case "adapter", "webhooks", "admission-webhooks", "metrics-server", "metrics-apiserver", "proxy", "sidecar", "e2e", "test", "tests", "example", "examples":
		return true
	default:
		return false
	}
}

func chooseDeterministicPackageName(f *RepoFacts, a *Analysis, main string, explicit string) string {
	if s := sanitizeName(explicit); s != "" && s != "unknown" {
		return s
	}
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
	website := ""
	if a != nil && strings.TrimSpace(a.Metadata.Website) != "" {
		website = a.Metadata.Website
	} else if f != nil {
		website = f.Website
	}
	if f != nil && looksInstallScriptURL(website) && strings.TrimSpace(f.GitRemoteURL) != "" {
		return normalizeRepoURL(f.GitRemoteURL)
	}
	return website
}

// chooseBuildStyle delegates to selectStrategy (helpers.go) as the single
// source of truth rather than duplicating the switch logic here.
func chooseBuildStyle(f *RepoFacts, a *Analysis, explicit string) string {
	if s := strings.TrimSpace(explicit); s != "" {
		return s
	}
	if looksContainerAssemblyRepo(f, a) {
		return "container-assembly"
	}
	if s := preferredMixedBuildStyle(f, a); s != "" {
		return s
	}
	if shouldPreferSelectedStrategy(f, a) {
		return strings.TrimSpace(a.SelectedStrategy)
	}
	if f != nil {
		if f.HasGoMod && !hasStrongRuntimeEvidence(a) {
			return "generic-placeholder"
		}
		if f.HasCargoToml && strings.TrimSpace(f.CargoBinName) == "" && strings.TrimSpace(f.CargoPackageName) == "" && !hasStrongRuntimeEvidence(a) {
			return "generic-placeholder"
		}
		if (f.PrimaryType == "go" || f.PrimaryType == "rust") && !hasLikelyRuntime(f, a) {
			return "generic-placeholder"
		}
	}
	// Prefer analysis alternatives — they carry scored evidence from the full
	// repo scan and are more reliable than a simple manifest check.
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
	return selectStrategy(f, a)
}

func preferredMixedBuildStyle(f *RepoFacts, a *Analysis) string {
	if f == nil {
		return ""
	}
	if prefersRustForNodeWrapperRepo(f) {
		if cargoWorkspace(f.RepoDir) || (a != nil && a.RepoShape.HasWorkspace) {
			return "rust-workspace"
		}
		return "rust-simple"
	}
	return ""
}

func hasStrongRuntimeEvidence(a *Analysis) bool {
	if a == nil {
		return false
	}
	if len(a.Services) > 0 {
		return true
	}
	return normalizeConfidence(a.Runtime.Confidence) != "low" && strings.TrimSpace(a.Runtime.Entrypoint) != ""
}

func shouldPreferSelectedStrategy(f *RepoFacts, a *Analysis) bool {
	if f == nil || a == nil {
		return false
	}
	selected := strings.TrimSpace(a.SelectedStrategy)
	if selected == "" || selected == "generic-placeholder" || selected == "container-assembly" {
		return false
	}
	languageCount := len(dedupeStrings(append([]string(nil), a.Languages...)))
	if languageCount < 2 {
		for _, present := range []bool{f.HasGoMod, f.HasCargoToml, f.HasPackageJSON, f.HasPyProject || f.HasRequirements || f.HasSetupPy} {
			if present {
				languageCount++
			}
		}
	}
	if languageCount < 2 {
		return false
	}
	return (strings.HasPrefix(selected, "rust") && f.HasCargoToml) ||
		(strings.HasPrefix(selected, "go") && f.HasGoMod) ||
		(strings.HasPrefix(selected, "python") && (f.HasPyProject || f.HasRequirements || f.HasSetupPy))
}

func chooseRuntimeDefaults(f *RepoFacts, a *Analysis, main string) (string, string) {
	main = sanitizeName(main)
	if main != "" && main != "unknown" {
		switch {
		case f != nil && (f.PrimaryType == "go" || f.PrimaryType == "rust") && hasLikelyRuntime(f, a):
			return main, "--help"
		case f != nil && f.PrimaryType == "unknown":
			return main, ""
		case f != nil && f.PrimaryType == "node" && hasLikelyRuntime(f, a):
			if strings.TrimSpace(f.NodeMain) != "" && main == "node" {
				return "node", f.NodeMain
			}
			return main, ""
		case f != nil && f.PrimaryType == "python" && hasLikelyRuntime(f, a):
			if strings.TrimSpace(f.PythonModuleName) != "" && main == "python3" {
				return "python3", "-m " + f.PythonModuleName
			}
			return main, ""
		}
	}

	if hasLikelyRuntime(f, a) && a != nil && strings.TrimSpace(a.Runtime.Entrypoint) != "" {
		return a.Runtime.Entrypoint, a.Runtime.Cmd
	}

	if hasLikelyRuntime(f, a) && a != nil && a.Alternatives != nil && len(a.Alternatives.EntryPoints) > 0 {
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
		if hasLikelyRuntime(f, a) {
			return sanitizeName(main), "--help"
		}
	case "node":
		if hasLikelyRuntime(f, a) && f.NodeMain != "" {
			return "node", f.NodeMain
		}
		if hasLikelyRuntime(f, a) && f.NodeBinName != "" {
			return f.NodeBinName, ""
		}
	case "python":
		if hasLikelyRuntime(f, a) && f.PythonConsoleScript != "" {
			return f.PythonConsoleScript, ""
		}
		if hasLikelyRuntime(f, a) && f.PythonModuleName != "" {
			return "python3", "-m " + f.PythonModuleName
		}
	}

	return "", ""
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

	installedName := sanitizeName(plan.PrimaryBinaryName)
	if installedName == "" || installedName == "unknown" {
		installedName = sanitizeName(plan.MainComponent)
	}
	if installedName == "" || installedName == "unknown" {
		installedName = sanitizeName(plan.PackageName)
	}
	if installedName == "" || installedName == "unknown" {
		installedName = "app"
	}

	buildTargetName := sanitizeName(plan.MainComponent)
	if buildTargetName == "" || buildTargetName == "unknown" {
		buildTargetName = installedName
	}
	buildTarget := strings.TrimSpace(plan.PrimaryBuildTarget)
	if buildTarget == "" {
		buildTarget = chooseGoBuildTarget(f, buildTargetName)
	}

	if plan.Intent != IntentContainerOnly {
		switch {
		case strings.HasPrefix(plan.BuildStyle, "go") && hasLikelyRuntime(f, a):
			path := nativeBinaryArtifactPath(installedName)
			if plan.Intent == IntentWindowsCross || plan.TargetFamily == TargetFamilyWindows {
				path += ".exe"
			}
			out = append(out, PlannedArtifact{
				Kind:        "binary",
				Path:        path,
				Name:        installedName,
				BuildTarget: buildTarget,
				Required:    true,
				Confidence:  normalizeConfidence("high"),
				Reason:      "primary Go package binary",
			})

		case strings.HasPrefix(plan.BuildStyle, "rust") && hasLikelyRuntime(f, a):
			add("binary", "src/target/release/"+installedName, "", installedName, "", "high", "primary Cargo package binary", true)
		}
	}

	if a != nil && shouldEmitManpagesInBaseline(a, plan) {
		for _, pe := range a.InstallLayout.Manpages {
			add("manpage", pe.Path, "", filepath.Base(pe.Path), "", pe.Confidence, pe.Reason, false)
		}
	}

	if a != nil {
		for _, pe := range a.InstallLayout.Docs {
			if isDirectoryLikeArtifactPath(pe.Path) && !shouldEmitDocDirsInBaseline(a, plan) {
				continue
			}
			add("doc", pe.Path, "", filepath.Base(pe.Path), "", pe.Confidence, pe.Reason, false)
		}
	}

	if a != nil && len(a.InstallLayout.Systemd) > 0 {
		for _, pe := range a.InstallLayout.Systemd {
			add("systemd", pe.Path, "", filepath.Base(pe.Path), "", pe.Confidence, pe.Reason, false)
		}
	}
	if a != nil {
		for _, pe := range a.InstallLayout.ConfigFiles {
			add("config", pe.Path, "", filepath.Base(pe.Path), "", pe.Confidence, pe.Reason, false)
		}
		for _, pe := range a.InstallLayout.DataDirs {
			add("data_dir", pe.Path, "", filepath.Base(pe.Path), "", pe.Confidence, pe.Reason, false)
		}
		for _, pe := range a.InstallLayout.Libexec {
			add("libexec", pe.Path, "", filepath.Base(pe.Path), "", pe.Confidence, pe.Reason, false)
		}
	}
	if f != nil && strings.TrimSpace(f.LicensePath) != "" && plan.Intent != IntentContainerOnly {
		add("license", filepath.ToSlash(strings.TrimSpace(f.LicensePath)), "", filepath.Base(f.LicensePath), "", "medium", "license file discovered in repository", false)
	}

	return dedupePlannedArtifacts(out)
}

func filterArtifactsForRequestedBinaries(in []PlannedArtifact, names []string) []PlannedArtifact {
	if len(names) == 0 {
		return in
	}
	wanted := make(map[string]struct{}, len(names))
	for _, n := range names {
		if n = sanitizeName(n); n != "" && n != "unknown" {
			wanted[n] = struct{}{}
		}
	}

	var out []PlannedArtifact
	for _, art := range in {
		if strings.TrimSpace(art.Kind) != "binary" {
			out = append(out, art)
			continue
		}
		if _, ok := wanted[artifactInstalledName(art, "")]; ok {
			out = append(out, art)
		}
	}
	return dedupePlannedArtifacts(out)
}

func plannedGoBinaryArtifacts(names []string) []PlannedArtifact {
	var out []PlannedArtifact
	for _, n := range names {
		n = sanitizeName(n)
		if n == "" || n == "unknown" {
			continue
		}
		out = append(out, PlannedArtifact{
			Kind:       "binary",
			Name:       n,
			Path:       nativeBinaryArtifactPath(n),
			Confidence: "high",
			Reason:     "explicit user-requested Go binary",
		})
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
		if f != nil && !f.GoNeedsManagedToolchain {
			add("build", "golang", "high", "Go repository detected")
		}
		if f != nil && f.HasMakefile {
			add("build", "make", "high", "top-level Makefile detected")
		}
		if f != nil && f.GoNeedsManagedToolchain {
			add("build", "ca-certificates", "medium", "managed Go toolchain download")
			add("build", "tar", "medium", "managed Go toolchain extraction")
		}
	case strings.HasPrefix(plan.BuildStyle, "rust"):
		add("build", "rust", "high", "Cargo repository detected")
		add("build", "cargo", "high", "Cargo repository detected")
	case strings.HasPrefix(plan.BuildStyle, "node"):
		add("build", "nodejs", "high", "Node package manifest detected")
		add("runtime", "nodejs", "medium", "Node application runtime")
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
		add("runtime", "python3", "medium", "Python application runtime")
	case strings.HasPrefix(plan.BuildStyle, "generic-make"):
		add("build", "make", "high", "top-level Makefile detected")
	case plan.Intent == IntentContainerOnly:
		// No default build deps; container-only may be pure assembly.
	}
	if hasPlannedArtifactKind(plan.Artifacts, "systemd") {
		add("runtime", "systemd", "low", "systemd service unit detected")
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
			}
		}

		for range a.ManpagePaths {
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
	if hasLikelyRuntime(f, a) {
		return true
	}
	if a != nil && (len(a.Services) > 0 || len(a.TestHints) > 0) {
		return true
	}

	return false
}

func deterministicTests(f *RepoFacts, a *Analysis, plan *SpecPlan) []PlannedTest {
	name := sanitizeName(plan.PrimaryBinaryName)
	if name == "" || name == "unknown" {
		name = sanitizeName(plan.MainComponent)
	}
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

	smokeBin := plannedSmokeBinary(plan)
	if smokeBin == "" {
		smokeBin = sanitizeName(plan.Entrypoint)
	}

	if smokeBin != "" {
		installedBin := "/usr/bin/" + smokeBin
		cmd := installedBin
		if strings.TrimSpace(plan.Cmd) != "" {
			cmd += " " + strings.TrimSpace(plan.Cmd)
		}

		conf := "low"
		reason := "runtime smoke test generated from entrypoint"
		if strings.HasPrefix(plan.BuildStyle, "go") || strings.HasPrefix(plan.BuildStyle, "rust") {
			conf = "medium"
			reason = "runtime smoke test generated from planned binary artifact"
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

func plannedSmokeBinary(plan *SpecPlan) string {
	if plan == nil {
		return ""
	}
	for _, art := range plan.Artifacts {
		if strings.TrimSpace(art.Kind) == "binary" {
			return artifactInstalledName(art, sanitizeName(plan.PrimaryBinaryName))
		}
	}
	return ""
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
	src = filepath.ToSlash(strings.TrimSpace(src))
	base := filepath.Base(src)
	if base == "" {
		base = "app.conf"
	}
	if strings.HasPrefix(src, "etc/") {
		return "/" + strings.TrimPrefix(src, "./")
	}
	if strings.HasPrefix(base, "etc_") {
		named := strings.TrimPrefix(base, "etc_")
		if named == "" {
			named = base
		}
		return "/etc/" + strings.ReplaceAll(named, "_", "/")
	}
	return "/etc/" + base
}

func dedupePlannedArtifacts(in []PlannedArtifact) []PlannedArtifact {
	seen := map[string]struct{}{}
	out := make([]PlannedArtifact, 0, len(in))
	for _, item := range in {
		key := item.Kind + "|" + item.Path + "|" + item.Target + "|" + item.BuildTarget
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
