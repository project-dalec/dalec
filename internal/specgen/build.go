package specgen

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/project-dalec/dalec"
)

func BuildSpec(ctx context.Context, a *Analysis, plan *SpecPlan, sourceMode string) (*dalec.Spec, []string, error) {
	_ = ctx

	if a == nil || a.Facts == nil {
		return nil, nil, fmt.Errorf("analysis is nil")
	}
	if plan == nil {
		return nil, nil, fmt.Errorf("plan is nil")
	}

	spec := &dalec.Spec{
		Name:        chooseInitialSpecNameFromPlan(a, plan),
		Version:     baselineSpecVersionExpr(a, plan),
		Revision:    baselineSpecRevisionExpr(a, plan),
		Packager:    "Dalec SpecGen",
		Vendor:      "Dalec SpecGen",
		License:     firstNonEmpty(plan.License, a.Metadata.License),
		Description: firstNonEmpty(plan.Description, a.Metadata.Description),
		Website:     firstNonEmpty(plan.Website, a.Metadata.Website),
	}

	src, warnings := newSourceFromPlan(sourceMode, a, plan)
	spec.Sources = map[string]dalec.Source{
		"src": src,
	}

	applyPlanArgs(spec, a, plan, &warnings)

	emissionPlan := preparePlanForEmission(a, plan, &warnings)

	applySourceGenerators(spec, a, emissionPlan, &warnings)
	applyPlannedDependencies(spec, emissionPlan, &warnings)
	applyPlannedArtifacts(spec, emissionPlan, &warnings)
	applyPlannedImage(spec, emissionPlan)
	applyPlannedTests(spec, emissionPlan, &warnings)

	buildByPlan(spec, a, emissionPlan, &warnings)

	return spec, dedupeWarnings(warnings), nil
}

func baselineSpecVersionValue(a *Analysis, plan *SpecPlan) string {
	if a != nil && strings.TrimSpace(a.Metadata.Version) != "" && !looksTodo(a.Metadata.Version) {
		return strings.TrimSpace(a.Metadata.Version)
	}
	if plan != nil && plan.Args != nil {
		if v, ok := plan.Args["VERSION"]; ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return "0.1.0"
}

func baselineSpecRevisionValue(a *Analysis, plan *SpecPlan) string {
	if plan != nil && plan.Args != nil {
		if v, ok := plan.Args["REVISION"]; ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return "1"
}

func baselineSpecVersionExpr(a *Analysis, plan *SpecPlan) string {
	if plan != nil && plan.Args != nil {
		if _, ok := plan.Args["VERSION"]; ok {
			return "${VERSION}"
		}
	}
	return baselineSpecVersionValue(a, plan)
}

func baselineSpecRevisionExpr(a *Analysis, plan *SpecPlan) string {
	if plan != nil && plan.Args != nil {
		if _, ok := plan.Args["REVISION"]; ok {
			return "${REVISION}"
		}
	}
	return baselineSpecRevisionValue(a, plan)
}

func applyPlanArgs(spec *dalec.Spec, a *Analysis, plan *SpecPlan, warnings *[]string) {
	if spec == nil || plan == nil {
		return
	}
	if len(plan.Args) == 0 {
		return
	}

	// Assumes dalec.Spec.Args is map[string]string in your tree.
	// If your repo uses a slightly different type, only this helper should need adjustment.
	if spec.Args == nil {
		spec.Args = map[string]string{}
	}

	for k, v := range plan.Args {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}

		switch k {
		case "VERSION":
			if strings.TrimSpace(v) == "" {
				v = baselineSpecVersionValue(a, plan)
			}
		case "REVISION":
			if strings.TrimSpace(v) == "" {
				v = baselineSpecRevisionValue(a, plan)
			}
		}

		spec.Args[k] = strings.TrimSpace(v)
	}

	if _, ok := spec.Args["VERSION"]; ok && spec.Version != "${VERSION}" {
		spec.Version = "${VERSION}"
	}
	if _, ok := spec.Args["REVISION"]; ok && spec.Revision != "${REVISION}" {
		spec.Revision = "${REVISION}"
	}
}

func newSourceFromPlan(sourceMode string, a *Analysis, plan *SpecPlan) (dalec.Source, []string) {
	var warnings []string

	src := dalec.Source{
		Context: &dalec.SourceContext{},
		Path:    ".",
	}

	switch strings.ToLower(strings.TrimSpace(sourceMode)) {
	case "", "context":
		return src, warnings
	case "git":
		warnings = append(warnings, "source=git requested, but baseline currently emits a context source until git-backed source emission is wired to verified repo URL/commit metadata")
		return src, warnings
	default:
		warnings = append(warnings, fmt.Sprintf("unknown --source=%q, defaulting to context", sourceMode))
		return src, warnings
	}
}

func preparePlanForEmission(a *Analysis, plan *SpecPlan, warnings *[]string) *SpecPlan {
	if plan == nil {
		return nil
	}

	cp := *plan
	cp.Artifacts = filterPlannedArtifactsForBaselineBuild(a, plan, warnings)
	cp.Dependencies = filterPlannedDependenciesForBaselineBuild(a, &cp, warnings)
	if cp.GenerateTests {
		cp.Tests = filterPlannedTestsForArtifacts(cp.Tests, cp.Artifacts, warnings)
	}

	return &cp
}

func filterPlannedArtifactsForBaselineBuild(a *Analysis, plan *SpecPlan, warnings *[]string) []PlannedArtifact {
	if plan == nil {
		return nil
	}

	var out []PlannedArtifact
	allowManpages := shouldEmitManpagesInBaseline(a, plan)
	allowDocDirs := shouldEmitDocDirsInBaseline(a, plan)

	for _, item := range plan.Artifacts {
		path := normalizeArtifactPath(item.Path)
		if path == "" {
			continue
		}

		switch item.Kind {
		case "binary":
			out = append(out, item)

		case "manpage":
			if strings.Contains(path, "*") {
				*warnings = append(*warnings, fmt.Sprintf("dropping wildcard manpage artifact %q from baseline; prefer concrete manpage paths only", path))
				continue
			}
			if !allowManpages {
				*warnings = append(*warnings, fmt.Sprintf("dropping manpage artifact %q because the selected baseline build does not currently produce manpages", path))
				continue
			}
			out = append(out, item)

		case "doc":
			if isDirectoryLikeArtifactPath(path) && !allowDocDirs {
				*warnings = append(*warnings, fmt.Sprintf("dropping broad docs artifact %q from baseline; keeping only concrete static docs until build/install evidence is stronger", path))
				continue
			}
			if strings.Contains(path, "*") {
				*warnings = append(*warnings, fmt.Sprintf("dropping wildcard doc artifact %q from baseline", path))
				continue
			}
			out = append(out, item)

		case "config":
			if strings.Contains(path, "*") {
				*warnings = append(*warnings, fmt.Sprintf("dropping wildcard config artifact %q from baseline", path))
				continue
			}
			out = append(out, item)

		case "data_dir":
			out = append(out, item)

		case "libexec", "systemd":
			out = append(out, item)

		default:
			out = append(out, item)
		}
	}

	return dedupePlannedArtifacts(out)
}

func filterPlannedDependenciesForBaselineBuild(a *Analysis, plan *SpecPlan, warnings *[]string) []PlannedDependency {
	if plan == nil {
		return nil
	}

	var out []PlannedDependency
	allowManpages := shouldEmitManpagesInBaseline(a, plan)
	usingMake := false
	if a != nil && strings.HasPrefix(plan.BuildStyle, "go") {
		usingMake = shouldUseMakeForGoBuild(a, plan)
	}

	for _, dep := range plan.Dependencies {
		name := strings.ToLower(strings.TrimSpace(dep.Name))
		scope := strings.ToLower(strings.TrimSpace(dep.Scope))

		if name == "go-md2man" && !allowManpages {
			*warnings = append(*warnings, "dropping go-md2man from build deps because baseline is not emitting manpages for this build shape")
			continue
		}
		if name == "make" && strings.HasPrefix(plan.BuildStyle, "go") && !usingMake {
			continue
		}
		if scope == "test" && !plan.GenerateTests {
			continue
		}

		out = append(out, dep)
	}

	return dedupePlannedDeps(out)
}

func filterPlannedTestsForArtifacts(tests []PlannedTest, artifacts []PlannedArtifact, warnings *[]string) []PlannedTest {
	if len(tests) == 0 {
		return nil
	}

	allowedFiles := allowedInstalledFileAssertionsFromArtifacts(artifacts)
	var out []PlannedTest

	for _, pt := range tests {
		next := pt
		next.Files = map[string]string{}

		for path, expectation := range pt.Files {
			path = strings.TrimSpace(path)
			if path == "" {
				continue
			}
			if strings.Contains(path, "*") {
				*warnings = append(*warnings, fmt.Sprintf("dropping wildcard file assertion %q from test %q", path, pt.Name))
				continue
			}
			if _, ok := allowedFiles[path]; !ok {
				*warnings = append(*warnings, fmt.Sprintf("dropping file assertion %q from test %q because it does not map to a concrete emitted artifact", path, pt.Name))
				continue
			}
			next.Files[path] = expectation
		}

		hasSteps := len(next.Steps) > 0
		hasFiles := len(next.Files) > 0
		if !hasSteps && !hasFiles {
			*warnings = append(*warnings, fmt.Sprintf("dropping empty test %q after artifact-aware filtering", pt.Name))
			continue
		}

		out = append(out, next)
	}

	return dedupePlannedTests(out)
}

func allowedInstalledFileAssertionsFromArtifacts(artifacts []PlannedArtifact) map[string]struct{} {
	out := map[string]struct{}{}

	for _, art := range artifacts {
		switch art.Kind {
		case "binary":
			out["/usr/bin/"+artifactInstalledName(art, "app")] = struct{}{}
		case "manpage":
			path := normalizeArtifactPath(art.Path)
			if path != "" && !strings.Contains(path, "*") {
				out["/usr/share/man/"+manpageInstalledSubpath(path)] = struct{}{}
			}
		case "config":
			path := normalizeArtifactPath(art.Path)
			if path != "" && !strings.Contains(path, "*") {
				out[defaultConfigInstallPath(path)] = struct{}{}
			}
		case "systemd":
			path := normalizeArtifactPath(art.Path)
			if path != "" && !strings.Contains(path, "*") {
				out["/usr/lib/systemd/system/"+filepath.Base(path)] = struct{}{}
			}
		}
	}

	return out
}

func shouldEmitManpagesInBaseline(a *Analysis, plan *SpecPlan) bool {
	if plan == nil {
		return false
	}
	if plan.Intent == IntentContainerOnly {
		return false
	}

	if strings.HasPrefix(plan.BuildStyle, "go") {
		if a == nil {
			return false
		}
		if shouldUseMakeForGoBuild(a, plan) && hasManpageBuildHints(a) {
			return true
		}
		return hasConcreteManpageFiles(a)
	}

	if strings.Contains(strings.ToLower(plan.BuildStyle), "make") && a != nil && hasManpageBuildHints(a) {
		return true
	}

	return a != nil && hasConcreteManpageFiles(a)
}

func shouldEmitDocDirsInBaseline(a *Analysis, plan *SpecPlan) bool {
	if plan == nil {
		return false
	}
	if strings.HasPrefix(plan.BuildStyle, "python") {
		return true
	}
	if strings.Contains(strings.ToLower(plan.BuildStyle), "make") && a != nil {
		for _, h := range append(append([]CommandHint(nil), a.BuildHints...), a.InstallHints2...) {
			low := strings.ToLower(strings.TrimSpace(h.Command))
			if strings.Contains(low, "docs") || strings.Contains(low, "install-docs") {
				return true
			}
		}
	}
	return false
}

func hasConcreteManpageFiles(a *Analysis) bool {
	if a == nil {
		return false
	}
	for _, p := range a.ManpagePaths {
		p = strings.TrimSpace(p)
		if p == "" || strings.Contains(p, "*") {
			continue
		}
		return true
	}
	return false
}

func hasManpageBuildHints(a *Analysis) bool {
	if a == nil {
		return false
	}
	for _, h := range append(append([]CommandHint(nil), a.BuildHints...), a.DocHints...) {
		low := strings.ToLower(strings.TrimSpace(h.Command))
		if strings.Contains(low, "go-md2man") || strings.Contains(low, "md2man") || strings.Contains(low, "make man") {
			return true
		}
	}
	return false
}

func isDirectoryLikeArtifactPath(path string) bool {
	path = normalizeArtifactPath(path)
	if path == "" {
		return false
	}
	base := filepath.Base(path)
	return !strings.Contains(base, ".")
}

func applySourceGenerators(spec *dalec.Spec, a *Analysis, plan *SpecPlan, warnings *[]string) {
	adapter := selectBaselineAdapter(analysisFacts(a), plan)
	adapter.ConfigureSource(spec, a, plan, warnings)
}

func buildByPlan(spec *dalec.Spec, a *Analysis, plan *SpecPlan, warnings *[]string) {
	if spec == nil || a == nil || a.Facts == nil || plan == nil {
		return
	}

	if plan.Intent == IntentContainerOnly {
		*warnings = append(*warnings, "container-only intent selected; baseline leaves build empty instead of emitting a placeholder build step")
		return
	}

	adapter := selectBaselineAdapter(a.Facts, plan)
	env, cmd := adapter.EmitBuild(spec, a, plan, warnings)
	if strings.TrimSpace(cmd) == "" {
		*warnings = append(*warnings, "no deterministic build command could be inferred; leaving build empty for manual or AI refinement")
		return
	}

	spec.Build = dalec.ArtifactBuild{
		Env:   env,
		Steps: dalec.BuildStepList{{Command: cmd}},
	}
}

func buildGoByPlan(spec *dalec.Spec, a *Analysis, plan *SpecPlan, warnings *[]string, env map[string]string) string {
	f := a.Facts

	env["GOFLAGS"] = "-trimpath"

	if plan.Intent == IntentWindowsCross || plan.TargetFamily == TargetFamilyWindows {
		env["GOOS"] = "windows"
	}

	out := sanitizeName(plan.MainComponent)
	if out == "" || out == "unknown" {
		out = sanitizeName(spec.Name)
	}
	if out == "" || out == "unknown" {
		out = "app"
	}

	target := chooseGoBuildTarget(f, out)
	useMake := shouldUseMakeForGoBuild(a, plan)

	if useMake {
		ensureGoBuildDeps(spec, true)
		ensureGoImage(spec, out, plan)
		if len(spec.Artifacts.Binaries) == 0 {
			path := nativeBinaryArtifactPath(out)
			if plan.Intent == IntentWindowsCross || plan.TargetFamily == TargetFamilyWindows {
				path += ".exe"
			}
			spec.Artifacts.Binaries = map[string]dalec.ArtifactConfig{
				path: {},
			}
		}
		*warnings = append(*warnings, "go make-driven repo detected and Makefile looks native-build friendly; using make for baseline build")
		return "cd src\nmake\n"
	}

	ensureGoBuildDeps(spec, false)
	ensureGoNativeArtifacts(spec, out, plan)
	ensureGoImage(spec, out, plan)

	if f.HasMakefile {
		if makeLooksCrossRelease(a) {
			*warnings = append(*warnings, "Makefile release target outputs need confirmation before finalizing repo-specific artifact paths")
		}
		*warnings = append(*warnings, "go make-driven repo detected; using conservative native go build output until refinement")
	}

	outPath := nativeBinaryBuildRelPath(out)
	if plan.Intent == IntentWindowsCross || plan.TargetFamily == TargetFamilyWindows {
		outPath += ".exe"
	}

	return fmt.Sprintf("cd src\nmkdir -p bin\ngo build -trimpath -o %s %s\n", outPath, target)
}

func buildRustByPlan(spec *dalec.Spec, a *Analysis, plan *SpecPlan, warnings *[]string, env map[string]string) string {
	out := sanitizeName(plan.MainComponent)
	if out == "" || out == "unknown" {
		out = sanitizeName(spec.Name)
	}
	if out == "" || out == "unknown" {
		out = "app"
	}

	if spec.Dependencies == nil {
		spec.Dependencies = &dalec.PackageDependencies{}
	}
	if spec.Dependencies.Build == nil {
		spec.Dependencies.Build = dalec.PackageDependencyList{}
	}
	spec.Dependencies.Build["rust"] = dalec.PackageConstraints{}
	spec.Dependencies.Build["cargo"] = dalec.PackageConstraints{}

	if len(spec.Artifacts.Binaries) == 0 && plan.Intent != IntentContainerOnly {
		spec.Artifacts.Binaries = map[string]dalec.ArtifactConfig{
			"src/target/release/" + out: {},
		}
	}

	if spec.Image == nil {
		spec.Image = &dalec.ImageConfig{}
	}
	if strings.TrimSpace(spec.Image.Entrypoint) == "" {
		spec.Image.Entrypoint = out
	}
	if strings.TrimSpace(spec.Image.Cmd) == "" {
		spec.Image.Cmd = "--help"
	}

	return "cd src\ncargo build --release\n"
}

func buildNodeByPlan(spec *dalec.Spec, a *Analysis, plan *SpecPlan, warnings *[]string, env map[string]string) string {
	return buildNodeCommand(a.Facts)
}

func buildPythonByPlan(spec *dalec.Spec, a *Analysis, plan *SpecPlan, warnings *[]string, env map[string]string) string {
	f := a.Facts

	if spec.Dependencies == nil {
		spec.Dependencies = &dalec.PackageDependencies{}
	}
	if spec.Dependencies.Build == nil {
		spec.Dependencies.Build = dalec.PackageDependencyList{}
	}
	spec.Dependencies.Build["python3"] = dalec.PackageConstraints{}
	spec.Dependencies.Build["python3-pip"] = dalec.PackageConstraints{}

	if strings.Contains(plan.BuildStyle, "requirements") {
		return "cd src\npython3 -m pip install -U pip\npython3 -m pip install -r requirements.txt\n"
	}

	if len(spec.Artifacts.DataDirs) == 0 {
		spec.Artifacts.DataDirs = map[string]dalec.ArtifactConfig{
			"src/dist": {},
		}
	}

	if strings.TrimSpace(f.PythonConsoleScript) != "" {
		if spec.Image == nil {
			spec.Image = &dalec.ImageConfig{}
		}
		if strings.TrimSpace(spec.Image.Entrypoint) == "" {
			spec.Image.Entrypoint = f.PythonConsoleScript
		}
	}

	return "cd src\npython3 -m pip install -U pip\npython3 -m pip wheel -w dist .\n"
}

func shouldUseMakeForGoBuild(a *Analysis, plan *SpecPlan) bool {
	if a == nil || a.Facts == nil || !a.Facts.HasMakefile {
		return false
	}

	lowStyle := strings.ToLower(strings.TrimSpace(plan.BuildStyle))
	if strings.Contains(lowStyle, "make") && !strings.Contains(lowStyle, "cross") && !makeLooksCrossRelease(a) {
		for _, line := range append(append([]string{}, a.CICommands...), a.InstallHints...) {
			low := strings.ToLower(strings.TrimSpace(line))
			switch {
			case low == "make",
				strings.HasPrefix(low, "make "),
				strings.Contains(low, " make "),
				strings.Contains(low, "make build"),
				strings.Contains(low, "make all"),
				strings.Contains(low, "make install"),
				strings.Contains(low, "make binaries"),
				strings.Contains(low, "make local"):
				if !strings.Contains(low, "make release") {
					return true
				}
			}
		}
	}

	return false
}

func makeLooksCrossRelease(a *Analysis) bool {
	if a == nil {
		return false
	}

	lines := append([]string{}, a.CICommands...)
	lines = append(lines, a.InstallHints...)

	for _, line := range lines {
		low := strings.ToLower(strings.TrimSpace(line))
		if strings.Contains(low, "make release") {
			return true
		}
		if strings.Contains(low, "goos=") || strings.Contains(low, "goarch=") {
			return true
		}
		if strings.Contains(low, "_linux_") ||
			strings.Contains(low, "_darwin_") ||
			strings.Contains(low, "_windows_") {
			return true
		}
	}

	return false
}

func ensureGoBuildDeps(spec *dalec.Spec, useMake bool) {
	if spec == nil {
		return
	}
	if spec.Dependencies == nil {
		spec.Dependencies = &dalec.PackageDependencies{}
	}
	if spec.Dependencies.Build == nil {
		spec.Dependencies.Build = dalec.PackageDependencyList{}
	}

	next := dalec.PackageDependencyList{}
	for k, v := range spec.Dependencies.Build {
		nk := canonicalBuildDepNameForBaseline(k)
		if nk == "make" && !useMake {
			continue
		}
		next[nk] = v
	}

	next["golang"] = dalec.PackageConstraints{}
	if useMake {
		next["make"] = dalec.PackageConstraints{}
	}

	spec.Dependencies.Build = next
}

func canonicalBuildDepNameForBaseline(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "go", "golang-go":
		return "golang"
	default:
		return strings.TrimSpace(name)
	}
}

func ensureGoNativeArtifacts(spec *dalec.Spec, out string, plan *SpecPlan) {
	if spec == nil {
		return
	}
	if len(spec.Artifacts.Binaries) > 0 {
		return
	}

	path := nativeBinaryArtifactPath(out)
	if plan != nil && (plan.Intent == IntentWindowsCross || plan.TargetFamily == TargetFamilyWindows) {
		path += ".exe"
	}

	spec.Artifacts.Binaries = map[string]dalec.ArtifactConfig{
		path: {},
	}
}

func ensureGoImage(spec *dalec.Spec, out string, plan *SpecPlan) {
	if spec == nil {
		return
	}
	if spec.Image == nil {
		spec.Image = &dalec.ImageConfig{}
	}
	if strings.TrimSpace(spec.Image.Entrypoint) == "" {
		spec.Image.Entrypoint = out
	}
	if strings.TrimSpace(spec.Image.Cmd) == "" {
		if plan != nil && (plan.Intent == IntentWindowsCross || plan.TargetFamily == TargetFamilyWindows) {
			spec.Image.Cmd = ""
		} else {
			spec.Image.Cmd = "--help"
		}
	}
}

func buildNodeCommand(f *RepoFacts) string {
	var b strings.Builder
	b.WriteString("cd src\n")

	switch f.NodePackageManager {
	case "pnpm":
		b.WriteString("corepack enable\n")
		if f.HasPnpmLock {
			b.WriteString("pnpm install --frozen-lockfile\n")
		} else {
			b.WriteString("pnpm install\n")
		}
		if f.NodeHasBuild {
			b.WriteString("pnpm run build\n")
		}

	case "yarn":
		b.WriteString("corepack enable\n")
		if f.HasYarnLock {
			b.WriteString("yarn install --frozen-lockfile\n")
		} else {
			b.WriteString("yarn install\n")
		}
		if f.NodeHasBuild {
			b.WriteString("yarn build\n")
		}

	default:
		if f.HasPackageLock {
			b.WriteString("npm ci\n")
		} else {
			b.WriteString("npm install\n")
		}
		if f.NodeHasBuild {
			b.WriteString("npm run build\n")
		} else {
			b.WriteString("npm run build --if-present\n")
		}
	}

	return b.String()
}

func genericBuildCommand(f *RepoFacts) string {
	if f != nil && f.HasMakefile {
		return "cd src\nmake\n"
	}
	return ""
}

func applyPlannedDependencies(spec *dalec.Spec, plan *SpecPlan, warnings *[]string) {
	if spec == nil || plan == nil {
		return
	}

	if spec.Dependencies == nil {
		spec.Dependencies = &dalec.PackageDependencies{}
	}
	if spec.Dependencies.Build == nil {
		spec.Dependencies.Build = dalec.PackageDependencyList{}
	}
	if spec.Dependencies.Runtime == nil {
		spec.Dependencies.Runtime = dalec.PackageDependencyList{}
	}
	if spec.Dependencies.Test == nil {
		spec.Dependencies.Test = dalec.PackageDependencyList{}
	}

	for _, dep := range plan.Dependencies {
		if strings.TrimSpace(dep.Target) != "" {
			*warnings = append(*warnings, fmt.Sprintf("target-scoped dependency %q for target %q is not yet emitted into target sections; keeping baseline root spec conservative", dep.Name, dep.Target))
			continue
		}

		switch dep.Scope {
		case "runtime", "recommends", "sysext":
			spec.Dependencies.Runtime[dep.Name] = dalec.PackageConstraints{}
		case "test":
			spec.Dependencies.Test[dep.Name] = dalec.PackageConstraints{}
		default:
			spec.Dependencies.Build[dep.Name] = dalec.PackageConstraints{}
		}
	}
}

func applyPlannedArtifacts(spec *dalec.Spec, plan *SpecPlan, warnings *[]string) {
	if spec == nil || plan == nil {
		return
	}

	for _, item := range plan.Artifacts {
		if strings.TrimSpace(item.Target) != "" {
			if warnings != nil {
				*warnings = append(*warnings, fmt.Sprintf(
					"target-scoped artifact %q for target %q is not yet emitted into target sections; keeping baseline root spec conservative",
					item.Path,
					item.Target,
				))
			}
			continue
		}

		path := normalizeArtifactPath(item.Path)
		if path == "" {
			continue
		}

		cfg := artifactConfigFromPlannedArtifact(item)

		switch item.Kind {
		case "binary":
			if spec.Artifacts.Binaries == nil {
				spec.Artifacts.Binaries = map[string]dalec.ArtifactConfig{}
			}
			spec.Artifacts.Binaries[path] = cfg

		case "manpage":
			if spec.Artifacts.Manpages == nil {
				spec.Artifacts.Manpages = map[string]dalec.ArtifactConfig{}
			}
			spec.Artifacts.Manpages[path] = cfg

		case "doc":
			if spec.Artifacts.Docs == nil {
				spec.Artifacts.Docs = map[string]dalec.ArtifactConfig{}
			}
			spec.Artifacts.Docs[path] = cfg

		case "config":
			if spec.Artifacts.ConfigFiles == nil {
				spec.Artifacts.ConfigFiles = map[string]dalec.ArtifactConfig{}
			}
			spec.Artifacts.ConfigFiles[path] = cfg

		case "data_dir":
			if spec.Artifacts.DataDirs == nil {
				spec.Artifacts.DataDirs = map[string]dalec.ArtifactConfig{}
			}
			spec.Artifacts.DataDirs[path] = cfg

		case "libexec":
			if spec.Artifacts.Libexec == nil {
				spec.Artifacts.Libexec = map[string]dalec.ArtifactConfig{}
			}
			spec.Artifacts.Libexec[path] = cfg

		case "systemd":
			if spec.Artifacts.Systemd == nil {
				spec.Artifacts.Systemd = &dalec.SystemdConfiguration{}
			}
			if spec.Artifacts.Systemd.Units == nil {
				spec.Artifacts.Systemd.Units = map[string]dalec.SystemdUnitConfig{}
			}
			spec.Artifacts.Systemd.Units[path] = dalec.SystemdUnitConfig{
				Name:   strings.TrimSpace(item.Name),
				Enable: false,
				Start:  false,
			}

		default:
			if warnings != nil {
				*warnings = append(*warnings, fmt.Sprintf(
					"skipping unsupported planned artifact kind %q at %s",
					item.Kind,
					item.Path,
				))
			}
		}
	}
}

func artifactConfigFromPlannedArtifact(item PlannedArtifact) dalec.ArtifactConfig {
	cfg := dalec.ArtifactConfig{
		SubPath: strings.TrimSpace(item.Subpath),
		Name:    strings.TrimSpace(item.Name),
	}

	if item.Mode != 0 {
		cfg.Permissions = fs.FileMode(item.Mode)
	}
	if strings.TrimSpace(item.User) != "" {
		cfg.User = strings.TrimSpace(item.User)
	}
	if strings.TrimSpace(item.Group) != "" {
		cfg.Group = strings.TrimSpace(item.Group)
	}

	return cfg
}

func applyPlannedImage(spec *dalec.Spec, plan *SpecPlan) {
	if spec == nil || plan == nil {
		return
	}

	if plan.Intent == IntentPackage || plan.Intent == IntentSysext {
		if strings.TrimSpace(plan.Entrypoint) == "" {
			return
		}
	}

	if strings.TrimSpace(plan.Entrypoint) == "" && strings.TrimSpace(plan.Cmd) == "" {
		return
	}

	if spec.Image == nil {
		spec.Image = &dalec.ImageConfig{}
	}
	if strings.TrimSpace(plan.Entrypoint) != "" {
		spec.Image.Entrypoint = strings.TrimSpace(plan.Entrypoint)
	}
	if strings.TrimSpace(plan.Cmd) != "" {
		spec.Image.Cmd = strings.TrimSpace(plan.Cmd)
	}
}

func applyPlannedTests(spec *dalec.Spec, plan *SpecPlan, warnings *[]string) {
	if spec == nil || plan == nil || !plan.GenerateTests {
		return
	}

	for _, pt := range plan.Tests {
		if strings.TrimSpace(pt.Target) != "" {
			*warnings = append(*warnings, fmt.Sprintf("target-scoped test %q for target %q is not yet emitted into target sections; keeping baseline root spec conservative", pt.Name, pt.Target))
			continue
		}

		var steps []dalec.TestStep
		for _, cmd := range pt.Steps {
			cmd = strings.TrimSpace(cmd)
			if cmd == "" {
				continue
			}
			steps = append(steps, dalec.TestStep{Command: cmd})
		}

		files := map[string]dalec.FileCheckOutput{}
		for path, expectation := range pt.Files {
			expectation = strings.TrimSpace(expectation)
			if expectation == "" || strings.EqualFold(expectation, "exists") {
				files[path] = dalec.FileCheckOutput{}
				continue
			}
			files[path] = dalec.FileCheckOutput{
				CheckOutput: dalec.CheckOutput{
					Contains: []string{expectation},
				},
			}
		}

		test := &dalec.TestSpec{
			Name:  pt.Name,
			Dir:   pt.Dir,
			Steps: steps,
		}
		if len(files) > 0 {
			test.Files = files
		}
		spec.Tests = append(spec.Tests, test)
	}
}

func chooseInitialSpecNameFromPlan(a *Analysis, plan *SpecPlan) string {
	if plan != nil {
		if s := sanitizeName(plan.PackageName); s != "" && s != "unknown" {
			return s
		}
		if s := sanitizeName(plan.MainComponent); s != "" && s != "unknown" {
			return s
		}
	}

	if a == nil || a.Facts == nil {
		return "unknown"
	}

	name := sanitizeName(a.Metadata.Name)
	if name != "" && !looksSemanticMajorOnly(name) {
		return name
	}

	base := sanitizeName(filepath.Base(a.Facts.RepoDir))
	if base != "" && !looksSemanticMajorOnly(base) {
		return base
	}

	if name != "" {
		return name
	}
	return "unknown"
}

func chooseGoBuildTarget(f *RepoFacts, outBin string) string {
	if f == nil {
		return "."
	}

	outBin = sanitizeName(outBin)

	if len(f.GoMainCandidates) == 1 {
		rel := f.GoMainCandidates[0]
		if rel == "." || rel == "" {
			return "."
		}
		return "./" + strings.TrimPrefix(rel, "./")
	}

	for _, rel := range f.GoMainCandidates {
		if sanitizeName(filepath.Base(rel)) == outBin {
			return "./" + strings.TrimPrefix(rel, "./")
		}
	}

	if f.GoMainRel != "" && f.GoMainRel != "." {
		return "./" + strings.TrimPrefix(f.GoMainRel, "./")
	}

	return "."
}
