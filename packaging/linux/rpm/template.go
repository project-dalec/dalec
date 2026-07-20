package rpm

import (
	"bufio"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"text/template"

	"github.com/project-dalec/dalec"
)

const (
	gomodsName      = "__gomods"
	cargohomeName   = "__cargohome"
	pipDepsName     = "__pipdeps"
	buildScriptName = "build.sh"
)

var specTmpl = template.Must(template.New("spec").Funcs(tmplFuncs).Parse(strings.TrimSpace(`
{{.DisableStrip}}
Name: {{.Name}}
Version: {{.Version}}
Release: {{.Release}}%{?dist}
License: {{ .License }}
Summary: {{ .Description }}
{{ .DisableAutoReq }}
{{ optionalField "URL" .Website -}}
{{ optionalField "Vendor" .Vendor -}}
{{ optionalField "Packager" .Packager -}}
{{ if .NoArch }}
BuildArch: noarch
{{ end }}
{{- .Sources -}}
{{- .Conflicts -}}
{{- .Provides -}}
{{- .Replaces -}}
{{- .Requires -}}
{{- .Recommends -}}

%description
{{.Description}}

{{ .PrepareSources -}}
{{ .BuildSteps -}}
{{ .Install -}}
{{ .Post -}}
{{ .PreUn -}}
{{ .PostUn -}}
{{ .Files -}}
{{ .SubPackages -}}
{{ .Changelog -}}
`)))

func optionalField(key, value string) string {
	if value == "" {
		return ""
	}
	return key + ": " + value + "\n"
}

func writeUserGroupProvides(b *strings.Builder, artifacts dalec.Artifacts) {
	for _, user := range artifacts.Users {
		fmt.Fprintf(b, "Provides: user(%s)\n", user.Name)
	}
	for _, group := range artifacts.Groups {
		fmt.Fprintf(b, "Provides: group(%s)\n", group.Name)
	}
}

var tmplFuncs = map[string]any{
	"optionalField": optionalField,
}

type specWrapper struct {
	*dalec.Spec
	Target       string
	SourceFilter dalec.SourceFilterConfig
}

func (w *specWrapper) Changelog() (fmt.Stringer, error) {
	b := &strings.Builder{}

	if len(w.Spec.Changelog) == 0 {
		return b, nil
	}

	fmt.Fprintf(b, "%%changelog\n")
	for _, log := range w.Spec.Changelog {
		fmt.Fprintln(b, "* "+log.Date.Format("Mon Jan 2 2006")+" "+log.Author)

		for _, change := range log.Changes {
			fmt.Fprintln(b, "- "+change)
		}
	}

	b.WriteString("\n")
	return b, nil
}

func (w *specWrapper) Provides() fmt.Stringer {
	b := &strings.Builder{}

	provides := w.Spec.GetProvides(w.Target)

	for name, constraints := range dalec.SortedMapIter(provides) {
		writeDep(b, "Provides", name, constraints)
	}

	// Self-Provide `user(X)` / `group(X)` for every user/group this
	// package's scriptlets will create.
	//
	// Starting with rpm 4.19.0 (Sept 2023), rpm's build-time generators
	// emit synthetic `Requires: user(NAME)` / `Requires: group(NAME)`
	// from any non-root ownership declared via `%attr` / `%defattr` in
	// `%files` (we emit those for symlink artifacts with explicit
	// ownership). Older rpm versions did not emit these auto-Requires,
	// which is why this only surfaces on rpm-4.19+ targets such as
	// Azure Linux 4.
	//
	// The package's own `%post` scriptlet creates the user/group via
	// `adduser` / `groupadd`, so declaring matching `Provides:` lines is
	// a truthful statement about what installing this package
	// accomplishes and lets the resolver satisfy the synthetic Requires
	// against ourselves. This is a no-op on targets whose rpm doesn't
	// emit the auto-Requires (extra metadata only).
	//
	// We only self-Provide entries the spec has explicitly declared in
	// `artifacts.users` / `artifacts.groups`. A spec that references a
	// user via `%attr` without declaring it will still — correctly — fail
	// the resolver, surfacing the missing declaration.
	//
	// References:
	//   - The rpm commit that introduced this behavior (in rpm 4.19.0):
	//     "Autogenerate requires for users and groups in packages from
	//     file data" (Mar 2023):
	//     https://github.com/rpm-software-management/rpm/commit/19ffee4f6a03ca3b6f55aaa2d579611bc9bb6b5e
	//   - rpm 4.19.0 release notes, "Package building → Spec":
	//     "Generate user/group requires from %files (#1032)":
	//     https://rpm.org/releases/4.19.0
	//   - rpm-sysusers(7), "DEPENDENCIES" section, current doc covering
	//     the %attr → user()/group() Requires generation:
	//     https://rpm-software-management.github.io/rpm/man/rpm-sysusers.7
	writeUserGroupProvides(b, w.Spec.GetArtifacts(w.Target))

	if b.Len() > 0 {
		b.WriteString("\n")
	}
	return b
}

func (w *specWrapper) Replaces() fmt.Stringer {
	b := &strings.Builder{}

	replaces := w.Spec.GetReplaces(w.Target)
	if len(replaces) == 0 {
		return b
	}

	keys := dalec.SortMapKeys(replaces)
	for _, name := range keys {
		writeDep(b, "Obsoletes", name, replaces[name])
	}
	return b
}

func (w *specWrapper) Conflicts() fmt.Stringer {
	b := &strings.Builder{}

	conflicts := w.Spec.GetConflicts(w.Target)
	if len(conflicts) == 0 {
		return b
	}

	keys := dalec.SortMapKeys(conflicts)
	for _, name := range keys {
		writeDep(b, "Conflicts", name, conflicts[name])
	}
	b.WriteString("\n")
	return b
}

func getSystemdRequires(cfg *dalec.SystemdConfiguration) string {
	var requires, orderRequires string
	if cfg.IsEmpty() {
		return ""
	}

	enabledUnits := cfg.EnabledUnits()
	if len(enabledUnits) > 0 {
		// if we are enabling any units, we need to require systemd
		// specifically for %post
		requires += "Requires(post): systemd\n"
		orderRequires += "OrderWithRequires(post): systemd\n"
	}

	// in any case where we have units as artifacts, we must require systemd
	// for %preun and %postun, as we are using the rpm systemd macros
	// in those stages which depend on systemctl
	requires +=
		`Requires(preun): systemd
Requires(postun): systemd
`

	orderRequires +=
		`OrderWithRequires(preun): systemd
OrderWithRequires(postun): systemd
`

	return requires + orderRequires
}

func getUserPostRequires(users []dalec.AddUserConfig, groups []dalec.AddGroupConfig) string {
	var out string

	if len(users) > 0 {
		out += "Requires(post): /usr/sbin/adduser, /usr/bin/getent\n"
	}
	if len(groups) > 0 {
		out += "Requires(post): /usr/sbin/groupadd, /usr/bin/getent\n"
	}

	return out
}

// We need this because AlmaLinux9 do not have chown/chgrp at install time.
// However, AzureLinux cannot resolve the /usr/bin/chown requirement.
// Thus, we just require coreutils which provides chown/chgrp on all distros hopefully.
func getOwnershipPostRequires(artifacts dalec.Artifacts) string {
	if hasOwnershipCommands(artifacts) {
		return "Requires(post): coreutils\n"
	}
	return ""
}

func ownershipRequested(user, group string) bool {
	return user != "" || group != ""
}

func artifactMapHasOwnership(artifacts map[string]dalec.ArtifactConfig) bool {
	for _, cfg := range artifacts {
		if ownershipRequested(cfg.User, cfg.Group) {
			return true
		}
	}
	return false
}

func directoryMapHasOwnership(dirs map[string]dalec.ArtifactDirConfig) bool {
	for _, cfg := range dirs {
		if ownershipRequested(cfg.User, cfg.Group) {
			return true
		}
	}
	return false
}

func symlinkOwnershipChanges(artifacts dalec.Artifacts, link dalec.ArtifactSymlinkConfig) (user, group bool) {
	for _, candidate := range artifacts.Users {
		if candidate.Name == link.User {
			user = true
			break
		}
	}
	for _, candidate := range artifacts.Groups {
		if candidate.Name == link.Group {
			group = true
			break
		}
	}
	return user, group
}

func hasOwnershipCommands(artifacts dalec.Artifacts) bool {
	if artifactMapHasOwnership(artifacts.ConfigFiles) ||
		artifactMapHasOwnership(artifacts.DataDirs) ||
		artifactMapHasOwnership(artifacts.Libs) ||
		artifactMapHasOwnership(artifacts.Binaries) ||
		artifactMapHasOwnership(artifacts.Libexec) {
		return true
	}

	if artifacts.Directories != nil &&
		(directoryMapHasOwnership(artifacts.Directories.Config) ||
			directoryMapHasOwnership(artifacts.Directories.State)) {
		return true
	}

	for _, link := range artifacts.Links {
		user, group := symlinkOwnershipChanges(artifacts, link)
		if user || group {
			return true
		}
	}
	return false
}

func (w *specWrapper) Requires() fmt.Stringer {
	b := &strings.Builder{}

	artifacts := w.Spec.GetArtifacts(w.Target)

	// first write post requires for systemd and user/group creation
	// as these do not come from dependencies in the spec
	// NOTE: This is a bit janky since different distributions may have different
	// package names... something to consider as we expand functionality.
	b.WriteString(getSystemdRequires(artifacts.Systemd))
	b.WriteString(getUserPostRequires(artifacts.Users, artifacts.Groups))
	b.WriteString(getOwnershipPostRequires(artifacts))

	deps := w.GetPackageDeps(w.Target)
	buildDeps := deps.GetBuild()
	runtimeDeps := deps.GetRuntime()
	if len(buildDeps) == 0 && len(runtimeDeps) == 0 {
		return b
	}

	buildKeys := dalec.SortMapKeys(buildDeps)
	for _, name := range buildKeys {
		constraints := buildDeps[name]
		writeDep(b, "BuildRequires", name, constraints)
	}

	if len(buildDeps) > 0 && len(runtimeDeps) > 0 {
		b.WriteString("\n")
	}

	runtimeKeys := dalec.SortMapKeys(runtimeDeps)
	for _, name := range runtimeKeys {
		constraints := runtimeDeps[name]
		// TODO: consider if it makes sense to support sources satisfying runtime deps
		writeDep(b, "Requires", name, constraints)
	}

	b.WriteString("\n")
	return b
}

func (w *specWrapper) Recommends() fmt.Stringer {
	b := &strings.Builder{}
	deps := w.GetPackageDeps(w.Target).GetRecommends()
	if len(deps) == 0 {
		return b
	}

	keys := dalec.SortMapKeys(deps)
	for _, name := range keys {
		constraints := deps[name]
		writeDep(b, "Recommends", name, constraints)
	}
	b.WriteString("\n")
	return b
}

// NOTE: This is very basic and does not handle things like grouped constraints
// Given this is just trying to shim things to allow either the rpm format or the deb format
// in its basic form, this is sufficient for now.
func FormatVersionConstraint(v string) string {
	prefix, suffix, ok := strings.Cut(v, " ")
	if !ok {
		if len(prefix) >= 1 {
			_, err := strconv.Atoi(prefix[:1])
			if err == nil {
				// This is just a version number, assume it should use the equal symbol
				return "== " + v
			}
		}
		return v
	}

	switch prefix {
	case "<<":
		return "< " + suffix
	case ">>":
		return "> " + suffix
	case "=":
		return "== " + suffix
	default:
		return v
	}
}

func writeDep(b *strings.Builder, kind, name string, constraints dalec.PackageConstraints) {
	do := func() {
		if len(constraints.Version) == 0 {
			fmt.Fprintf(b, "%s: %s\n", kind, name)
			return
		}

		for _, c := range constraints.Version {
			fmt.Fprintf(b, "%s: %s %s\n", kind, name, FormatVersionConstraint(c))
		}
	}

	if len(constraints.Arch) == 0 {
		do()
		return
	}

	for _, arch := range constraints.Arch {
		fmt.Fprintf(b, "%%ifarch %s\n", arch)
		do()
		fmt.Fprintf(b, "%%endif\n")
	}
}

func (w *specWrapper) Sources() (fmt.Stringer, error) {
	b := &strings.Builder{}

	// Sort keys for consistent output
	keys := dalec.SortMapKeys(w.Spec.Sources)

	for idx, name := range keys {
		src := w.Spec.Sources[name]
		ref := name
		isDir := dalec.SourceIsDir(src)

		if isDir {
			ref += ".tar.gz"
		}

		doc := src.Doc(name)
		scanner := bufio.NewScanner(doc)
		for scanner.Scan() {
			fmt.Fprintf(b, "# %s\n", scanner.Text())
		}
		if scanner.Err() != nil {
			return nil, scanner.Err()
		}
		if !w.SourceFilter.IsEmpty() && isDir {
			if err := docSourceFilter(b, "Exclusions", w.SourceFilter.GlobalExcludes); err != nil {
				return nil, err
			}
		}
		fmt.Fprintf(b, "Source%d: %s\n", idx, ref)
	}

	sourceIdx := len(keys)

	if w.Spec.HasGomods() {
		if !w.SourceFilter.IsEmpty() {
			if err := docSourceFilter(b, "Exclusions", w.SourceFilter.GlobalExcludes); err != nil {
				return nil, err
			}
		}
		fmt.Fprintf(b, "Source%d: %s.tar.gz\n", sourceIdx, gomodsName)
		sourceIdx += 1
	}

	if w.Spec.HasCargohomes() {
		if !w.SourceFilter.IsEmpty() {
			if err := docSourceFilter(b, "Exclusions", w.SourceFilter.GlobalExcludes); err != nil {
				return nil, err
			}
		}
		fmt.Fprintf(b, "Source%d: %s.tar.gz\n", sourceIdx, cargohomeName)
		sourceIdx += 1
	}

	if w.Spec.HasPips() {
		if !w.SourceFilter.IsEmpty() {
			if err := docSourceFilter(b, "Exclusions", w.SourceFilter.GlobalExcludes); err != nil {
				return nil, err
			}
		}
		fmt.Fprintf(b, "Source%d: %s.tar.gz\n", sourceIdx, pipDepsName)
		sourceIdx += 1
	}

	if len(w.Spec.Build.Steps) > 0 {
		fmt.Fprintf(b, "Source%d: %s\n", sourceIdx, buildScriptName)
	}

	if len(keys) > 0 {
		b.WriteString("\n")
	}
	return b, nil
}

func docSourceFilter(w io.Writer, name string, values []string) error {
	if _, err := fmt.Fprintf(w, "# %s:\n", name); err != nil {
		return err
	}
	for _, value := range values {
		scanner := bufio.NewScanner(strings.NewReader(value))
		for scanner.Scan() {
			if _, err := fmt.Fprintf(w, "# \t%s\n", scanner.Text()); err != nil {
				return err
			}
		}
		if err := scanner.Err(); err != nil {
			return err
		}
	}
	return nil
}

func (w *specWrapper) Release() string {
	if w.Spec.Revision == "" {
		return "1"
	}
	return w.Spec.Revision
}

func (w *specWrapper) PrepareSources() (fmt.Stringer, error) {
	b := &strings.Builder{}
	if len(w.Spec.Sources) == 0 {
		return b, nil
	}

	fmt.Fprintf(b, "%%prep\n")

	patches := make(map[string]bool)

	for _, v := range w.Spec.Patches {
		for _, p := range v {
			patches[p.Source] = true
		}
	}

	// Sort keys for consistent output
	keys := dalec.SortMapKeys(w.Spec.Sources)

	prepareGenerators := sync.OnceFunc(func() {
		if w.Spec.HasGomods() {
			fmt.Fprintf(b, "mkdir -p \"%%{_builddir}/%s\"\n", gomodsName)
			fmt.Fprintf(b, "tar -C \"%%{_builddir}/%s\" -xzf \"%%{_sourcedir}/%s.tar.gz\"\n", gomodsName, gomodsName)
		}
		if w.Spec.HasCargohomes() {
			fmt.Fprintf(b, "mkdir -p \"%%{_builddir}/%s\"\n", cargohomeName)
			fmt.Fprintf(b, "tar -C \"%%{_builddir}/%s\" -xzf \"%%{_sourcedir}/%s.tar.gz\"\n", cargohomeName, cargohomeName)
		}
		if w.Spec.HasPips() {
			fmt.Fprintf(b, "mkdir -p \"%%{_builddir}/%s\"\n", pipDepsName)
			fmt.Fprintf(b, "tar -C \"%%{_builddir}/%s\" -xzf \"%%{_sourcedir}/%s.tar.gz\"\n", pipDepsName, pipDepsName)
		}
	})

	// Extract all the sources from the rpm source dir
	for _, key := range keys {
		if !dalec.SourceIsDir(w.Spec.Sources[key]) {
			// This is a file, nothing to extract, but we need to put it into place
			// in  the rpm build dir
			fmt.Fprintf(b, "cp -a \"%%{_sourcedir}/%s\" .\n", key)
			continue
		}
		// This is a directory source so it needs to be untarred into the rpm build dir.
		fmt.Fprintf(b, "tar -C \"%%{_builddir}/\" -xzf \"%%{_sourcedir}/%s.tar.gz\"\n", key)
	}
	prepareGenerators()

	// Apply patches to all sources.
	// Note: These are applied based on the key sorting algorithm (lexicographic).
	//  Using one patch to patch another patch is not supported, except that it may
	//  occur if they happen to be sorted lexicographically.
	patchKeys := dalec.SortMapKeys(w.Spec.Patches)
	for _, key := range patchKeys {
		for _, patch := range w.Spec.Patches[key] {
			fmt.Fprintf(b, "patch -d %q -p%d -s --input \"%%{_builddir}/%s\"\n", key, *patch.Strip, filepath.Join(patch.Source, patch.Path))
		}
	}

	if len(keys) > 0 {
		b.WriteString("\n")
	}
	return b, nil
}

func writeStep(b *strings.Builder, step dalec.BuildStep) {
	envKeys := dalec.SortMapKeys(step.Env)
	// Wrap commands in a subshell so any environment variables that are set
	// will be available to every command in the BuildStep
	fmt.Fprintln(b, "(") // begin subshell
	for _, k := range envKeys {
		fmt.Fprintf(b, "export %s=\"%s\"\n", k, step.Env[k])
	}
	fmt.Fprintf(b, "%s", step.Command)
	fmt.Fprintln(b, ")") // end subshell
}

func (w *specWrapper) BuildSteps() fmt.Stringer {
	b := &strings.Builder{}

	if len(w.Spec.Build.Steps) == 0 {
		return b
	}

	fmt.Fprintf(b, "%%build\n")
	fmt.Fprintf(b, "%%{_sourcedir}/%s\n", buildScriptName)
	b.WriteString("\n")

	return b
}

func systemdPreUnScript(unitName string, cfg dalec.SystemdUnitConfig) string {
	if !cfg.Enable {
		return ""
	}

	return fmt.Sprintf("%%systemd_preun %s\n", unitName)
}

func preUnScriptBody(artifacts dalec.Artifacts) string {
	if artifacts.Systemd.IsEmpty() || (len(artifacts.Systemd.EnabledUnits()) == 0) {
		return ""
	}

	b := &strings.Builder{}
	for servicePath, unitConf := range dalec.SortedMapIter(artifacts.Systemd.Units) {
		serviceName := unitConf.Artifact().ResolveName(servicePath)
		b.WriteString(
			systemdPreUnScript(serviceName, unitConf),
		)
	}
	return b.String()
}

func (w *specWrapper) PreUn() fmt.Stringer {
	b := &strings.Builder{}

	body := preUnScriptBody(w.GetArtifacts(w.Target))
	if body == "" {
		return b
	}

	b.WriteString("%preun\n")
	b.WriteString(body)
	return b
}

func systemdPostScript(unitName string, cfg dalec.SystemdUnitConfig) string {
	if !cfg.Enable {
		return ""
	}

	// Use systemctl enable directly instead of %systemd_post because
	// %systemd_post calls "systemctl preset" which defers to system preset
	// policy. All RPM distros have "disable *" as a catch-all in their preset
	// files, so third-party services would never be enabled via preset.
	// This behavior may change in the future to respect system presets instead.
	// See https://github.com/project-dalec/dalec/issues/1017#issuecomment-4181051908
	//
	// The "|| :" ensures a non-zero exit (e.g. systemd not running in a
	// chroot/Kickstart/container) does not abort the scriptlet.
	// Only enable on initial install ($1 == 1), not upgrades.
	s := fmt.Sprintf(`if [ $1 -eq 1 ]; then
    systemctl enable %s || :
fi
`, unitName)

	if cfg.Start {
		// Only start on initial install ($1 == 1), not upgrades, to avoid
		// restarting a service the user intentionally stopped.
		// Guard behind a check for a running systemd so this is safe
		// in chroot/Kickstart/container environments.
		s += fmt.Sprintf(`if [ $1 -eq 1 ] && [ -d /run/systemd/system ]; then
    systemctl start %s || :
fi
`, unitName)
	}

	return s
}

func postScriptBody(artifacts dalec.Artifacts) string {
	b := &strings.Builder{}
	b.WriteString(systemdPostSection(artifacts))
	b.WriteString(postUsersScript(artifacts))
	b.WriteString(postGroupsScript(artifacts))
	b.WriteString(symlinkOwnershipScript(artifacts))
	b.WriteString(artifactOwnershipScript(artifacts))
	b.WriteString(directoryOwnershipScript(artifacts))
	b.WriteString(artifactCapabilitiesScript(artifacts))
	return b.String()
}

func (w *specWrapper) Post() fmt.Stringer {
	b := &strings.Builder{}

	body := postScriptBody(w.Spec.GetArtifacts(w.Target))
	if body == "" {
		return b
	}

	b.WriteString("%post\n")
	b.WriteString(body)
	b.WriteString("\n")
	return b
}

func postUsersScript(artifacts dalec.Artifacts) string {
	if len(artifacts.Users) == 0 {
		return ""
	}

	b := &strings.Builder{}
	for _, user := range artifacts.Users {
		fmt.Fprintf(b, "getent passwd %s >/dev/null || adduser %s\n", user.Name, user.Name)
	}
	return b.String()
}

func postGroupsScript(artifacts dalec.Artifacts) string {
	if len(artifacts.Groups) == 0 {
		return ""
	}

	b := &strings.Builder{}
	for _, group := range artifacts.Groups {
		fmt.Fprintf(b, "getent group %s >/dev/null || groupadd --system %s\n", group.Name, group.Name)
	}
	return b.String()
}

func directoryOwnershipScript(artifacts dalec.Artifacts) string {
	if artifacts.Directories == nil {
		return ""
	}
	b := &strings.Builder{}
	setDirOwnership := func(root, p string, cfg *dalec.ArtifactDirConfig) {
		if cfg == nil || !ownershipRequested(cfg.User, cfg.Group) {
			return
		}
		user := cfg.User
		group := cfg.Group
		targetDir := filepath.Join(root, p)
		if user != "" {
			fmt.Fprintf(b, "chown -R %s %s\n", user, targetDir)
		}
		if group != "" {
			fmt.Fprintf(b, "chgrp -R %s %s\n", group, targetDir)
		}
	}
	for p, cfg := range dalec.SortedMapIter(artifacts.Directories.Config) {
		setDirOwnership(`/%{_sysconfdir}`, p, &cfg)
	}
	for p, cfg := range dalec.SortedMapIter(artifacts.Directories.State) {
		setDirOwnership(`/%{_sharedstatedir}`, p, &cfg)
	}
	return b.String()
}

func artifactOwnershipScript(artifacts dalec.Artifacts) string {
	b := &strings.Builder{}

	setArtifactOwnership := func(root, p string, cfg *dalec.ArtifactConfig) {
		if cfg == nil || !ownershipRequested(cfg.User, cfg.Group) {
			return
		}
		user := cfg.User
		group := cfg.Group
		targetDir := filepath.Join(root, cfg.SubPath)
		var targetPath string
		file := cfg.ResolveName(p)
		if !strings.Contains(file, "*") {
			targetPath = filepath.Join(targetDir, file)
		} else {
			targetPath = targetDir + "/"
		}
		if user != "" {
			fmt.Fprintf(b, "chown -R %s %s\n", user, targetPath)
		}
		if group != "" {
			fmt.Fprintf(b, "chgrp -R %s %s\n", group, targetPath)
		}
	}

	if artifacts.ConfigFiles != nil {
		for c, cfg := range dalec.SortedMapIter(artifacts.ConfigFiles) {
			setArtifactOwnership(`/%{_sysconfdir}`, c, &cfg)
		}
	}
	if artifacts.DataDirs != nil {
		for k, df := range dalec.SortedMapIter(artifacts.DataDirs) {
			setArtifactOwnership(`/%{_datadir}`, k, &df)
		}
	}
	// Directory ownership is handled in getDirectoryOwnership; do not duplicate here.
	if artifacts.Libs != nil {
		for l, cfg := range dalec.SortedMapIter(artifacts.Libs) {
			setArtifactOwnership(`/%{_libdir}`, l, &cfg)
		}
	}
	if artifacts.Binaries != nil {
		for p, cfg := range dalec.SortedMapIter(artifacts.Binaries) {
			setArtifactOwnership(`/%{_bindir}`, p, &cfg)
		}
	}
	if artifacts.Libexec != nil {
		for p, cfg := range dalec.SortedMapIter(artifacts.Libexec) {
			setArtifactOwnership(`/%{_libexecdir}`, p, &cfg)
		}
	}

	return b.String()
}

func artifactCapabilitiesScript(artifacts dalec.Artifacts) string {
	b := &strings.Builder{}

	// Only use setcap in postinstall if there's also a chown/chgrp
	// (since chown clears capabilities). Otherwise, use %caps macro in %files.
	setArtifactCapabilities := func(root, p string, cfg *dalec.ArtifactConfig) {
		if cfg == nil {
			return
		}
		capString := dalec.CapabilitiesString(cfg.LinuxCapabilities)
		if capString == "" {
			return
		}
		// Only add setcap if there's a user/group ownership change
		if cfg.User == "" && cfg.Group == "" {
			return
		}
		targetDir := filepath.Join(root, cfg.SubPath)
		file := cfg.ResolveName(p)
		targetPath := filepath.Join(targetDir, file)
		fmt.Fprintf(b, "setcap '%s' %s\n", capString, targetPath)
	}

	if artifacts.Libs != nil {
		for l, cfg := range dalec.SortedMapIter(artifacts.Libs) {
			setArtifactCapabilities(`/%{_libdir}`, l, &cfg)
		}
	}
	if artifacts.Binaries != nil {
		for p, cfg := range dalec.SortedMapIter(artifacts.Binaries) {
			setArtifactCapabilities(`/%{_bindir}`, p, &cfg)
		}
	}
	if artifacts.Libexec != nil {
		for k, cfg := range dalec.SortedMapIter(artifacts.Libexec) {
			setArtifactCapabilities(`/%{_libexecdir}`, k, &cfg)
		}
	}

	return b.String()
}

func symlinkOwnershipScript(artifacts dalec.Artifacts) string {
	if len(artifacts.Links) == 0 {
		return ""
	}
	b := &strings.Builder{}

	for _, link := range artifacts.Links {
		user, group := symlinkOwnershipChanges(artifacts, link)
		if user {
			fmt.Fprintf(b, "chown -h %s %s\n", link.User, link.Dest)
		}
		if group {
			fmt.Fprintf(b, "chgrp -h %s %s\n", link.Group, link.Dest)
		}
	}
	return b.String()
}

func systemdPostSection(artifacts dalec.Artifacts) string {
	if artifacts.Systemd.IsEmpty() {
		return ""
	}
	enabledUnits := artifacts.Systemd.EnabledUnits()
	if len(enabledUnits) == 0 {
		// if we have no enabled units, we don't need to do anything systemd related
		// in the post script. In this case, we shouldn't emit '%post'
		// as this eliminates the need for extra dependencies in the target container
		return ""
	}
	b := &strings.Builder{}
	for servicePath := range dalec.SortedMapIter(enabledUnits) {
		unitConf := artifacts.Systemd.Units[servicePath]
		artifact := unitConf.Artifact()
		b.WriteString(
			systemdPostScript(artifact.ResolveName(servicePath), unitConf),
		)
	}

	return b.String()
}

func postUnScriptBody(artifacts dalec.Artifacts) string {
	if artifacts.Systemd.IsEmpty() {
		return ""
	}

	b := &strings.Builder{}
	for servicePath, cfg := range dalec.SortedMapIter(artifacts.Systemd.Units) {
		a := cfg.Artifact()
		serviceName := a.ResolveName(servicePath)
		fmt.Fprintf(b, "%%systemd_postun %s\n", serviceName)
	}
	return b.String()
}

func (w *specWrapper) PostUn() fmt.Stringer {
	b := &strings.Builder{}

	body := postUnScriptBody(w.GetArtifacts(w.Target))
	if body == "" {
		return b
	}

	b.WriteString("%postun\n")
	b.WriteString(body)
	return b
}

func (w *specWrapper) Install() fmt.Stringer {
	b := &strings.Builder{}
	fmt.Fprintln(b, "%install")

	artifacts := w.Spec.GetArtifacts(w.Target)
	installArtifacts(b, artifacts, w.Name)

	for key, pkg := range dalec.GetSubPackagesForTarget(w.Spec, w.Target) {
		if pkg.Artifacts != nil {
			resolvedName := pkg.ResolvedName(w.Spec.Name, key)
			installArtifacts(b, *pkg.Artifacts, resolvedName)
		}
	}

	b.WriteString("\n")
	return b
}

func installArtifacts(b *strings.Builder, artifacts dalec.Artifacts, pkgName string) {
	copyArtifact := func(root, p string, cfg *dalec.ArtifactConfig) {
		if cfg == nil {
			return
		}
		targetDir := filepath.Join(root, cfg.SubPath)
		fmt.Fprintln(b, "mkdir -p", targetDir)

		var targetPath string
		file := cfg.ResolveName(p)
		if !strings.Contains(file, "*") {
			targetPath = filepath.Join(targetDir, file)
		} else {
			targetPath = targetDir + "/"
		}
		fmt.Fprintln(b, "cp -r", p, targetPath)
		if cfg.Permissions.Perm() != 0 {
			fmt.Fprintf(b, "chmod %o %s\n", cfg.Permissions, targetPath)
		}
	}

	for p, cfg := range dalec.SortedMapIter(artifacts.Binaries) {
		copyArtifact(`%{buildroot}/%{_bindir}`, p, &cfg)
	}

	for p, cfg := range dalec.SortedMapIter(artifacts.Manpages) {
		copyArtifact(`%{buildroot}/%{_mandir}`, p, &cfg)
	}

	createArtifactDir := func(root, p string, cfg dalec.ArtifactDirConfig) {
		dir := filepath.Join(root, p)
		mkdirCmd := "mkdir"
		perms := cfg.Mode.Perm()
		if perms != 0 {
			mkdirCmd += fmt.Sprintf(" -m %o", cfg.Mode)
		}
		fmt.Fprintf(b, "%s -p %q\n", mkdirCmd, dir)
	}

	if artifacts.Directories != nil {
		for p, cfg := range dalec.SortedMapIter(artifacts.Directories.Config) {
			createArtifactDir(`%{buildroot}/%{_sysconfdir}`, p, cfg)
		}

		for p, cfg := range dalec.SortedMapIter(artifacts.Directories.State) {
			createArtifactDir(`%{buildroot}/%{_sharedstatedir}`, p, cfg)
		}
	}

	for k, df := range dalec.SortedMapIter(artifacts.DataDirs) {
		copyArtifact(`%{buildroot}/%{_datadir}`, k, &df)
	}

	if artifacts.Libexec != nil {
		for k, le := range dalec.SortedMapIter(artifacts.Libexec) {
			copyArtifact(`%{buildroot}/%{_libexecdir}`, k, &le)
		}
	}

	for c, cfg := range dalec.SortedMapIter(artifacts.ConfigFiles) {
		copyArtifact(`%{buildroot}/%{_sysconfdir}`, c, &cfg)
	}

	if artifacts.Systemd != nil {
		for p, cfg := range dalec.SortedMapIter(artifacts.Systemd.Units) {
			// must include systemd unit extension (.service, .socket, .timer, etc.) in name
			copyArtifact(`%{buildroot}/%{_unitdir}`, p, cfg.Artifact())
		}

		for d, cfg := range dalec.SortedMapIter(artifacts.Systemd.Dropins) {
			copyArtifact(`%{buildroot}/%{_unitdir}`, d, cfg.Artifact())
		}
	}

	for d, cfg := range dalec.SortedMapIter(artifacts.Docs) {
		root := filepath.Join(`%{buildroot}/%{_docdir}`, pkgName)
		copyArtifact(root, d, &cfg)
	}

	for l, cfg := range dalec.SortedMapIter(artifacts.Licenses) {
		root := filepath.Join(`%{buildroot}/%{_licensedir}`, pkgName)
		copyArtifact(root, l, &cfg)
	}

	for l, cfg := range dalec.SortedMapIter(artifacts.Libs) {
		root := filepath.Join(`%{buildroot}/%{_libdir}`)
		copyArtifact(root, l, &cfg)
	}

	for _, l := range artifacts.Links {
		fmt.Fprintln(b, "mkdir -p", filepath.Dir(filepath.Join("%{buildroot}", l.Dest)))
		fmt.Fprintln(b, "ln -sf", l.Source, "%{buildroot}/"+l.Dest)
	}

	for h, cfg := range dalec.SortedMapIter(artifacts.Headers) {
		copyArtifact(`%{buildroot}/%{_includedir}`, h, &cfg)
	}
}

func (w *specWrapper) Files() fmt.Stringer {
	b := &strings.Builder{}
	fmt.Fprintf(b, "%%files\n")
	writeFilesBody(b, w.GetArtifacts(w.Target), w.Name)
	b.WriteString("\n")
	return b
}

func writeFilesBody(b *strings.Builder, artifacts dalec.Artifacts, pkgName string) {
	for p, cfg := range dalec.SortedMapIter(artifacts.Binaries) {
		full := filepath.Join(`%{_bindir}/`, cfg.SubPath, cfg.ResolveName(p))
		// Use %caps macro if capabilities are set and there's no chown
		capString := dalec.CapabilitiesString(cfg.LinuxCapabilities)
		if capString != "" && cfg.User == "" && cfg.Group == "" {
			fmt.Fprintf(b, "%%caps(%s) %s\n", capString, full)
		} else {
			fmt.Fprintln(b, full)
		}
	}

	for p, cfg := range dalec.SortedMapIter(artifacts.Manpages) {
		path := filepath.Join(`%{_mandir}`, cfg.SubPath, cfg.ResolveName(p))
		fmt.Fprintln(b, path)
	}

	if artifacts.Directories != nil {
		for p := range dalec.SortedMapIter(artifacts.Directories.Config) {
			dir := strings.Join([]string{`%dir`, filepath.Join(`%{_sysconfdir}`, p)}, " ")
			fmt.Fprintln(b, dir)
		}

		for p := range dalec.SortedMapIter(artifacts.Directories.State) {
			dir := strings.Join([]string{`%dir`, filepath.Join(`%{_sharedstatedir}`, p)}, " ")
			fmt.Fprintln(b, dir)
		}
	}
	if artifacts.DataDirs != nil {
		for k, df := range dalec.SortedMapIter(artifacts.DataDirs) {
			fullPath := filepath.Join(`%{_datadir}`, df.SubPath, df.ResolveName(k))
			fmt.Fprintln(b, fullPath)
		}
	}
	if artifacts.Libexec != nil {
		for k, le := range dalec.SortedMapIter(artifacts.Libexec) {
			targetDir := filepath.Join(`%{_libexecdir}`, le.SubPath)
			fullPath := filepath.Join(targetDir, le.ResolveName(k))
			// Use %caps macro if capabilities are set and there's no chown
			capString := dalec.CapabilitiesString(le.LinuxCapabilities)
			if capString != "" && le.User == "" && le.Group == "" {
				fmt.Fprintf(b, "%%caps(%s) %s\n", capString, fullPath)
			} else {
				fmt.Fprintln(b, fullPath)
			}
		}
	}

	for c, cfg := range dalec.SortedMapIter(artifacts.ConfigFiles) {
		fullPath := filepath.Join(`%{_sysconfdir}`, cfg.SubPath, cfg.ResolveName(c))
		fullDirective := strings.Join([]string{`%config(noreplace)`, fullPath}, " ")
		fmt.Fprintln(b, fullDirective)
	}
	if artifacts.Systemd != nil {
		for p, cfg := range dalec.SortedMapIter(artifacts.Systemd.Units) {
			a := cfg.Artifact()
			unitPath := filepath.Join(`%{_unitdir}/`, a.SubPath, a.ResolveName(p))
			fmt.Fprintln(b, unitPath)
		}

		dropins := make(map[string][]string)
		// process these to get a unique list of files per unit name.
		// we need a single dir entry for the directory
		// need a file entry for each of files
		for d, cfg := range dalec.SortedMapIter(artifacts.Systemd.Dropins) {
			art := cfg.Artifact()
			files, ok := dropins[cfg.Unit]
			if !ok {
				files = []string{}
			}
			p := filepath.Join(
				`%{_unitdir}`,
				fmt.Sprintf("%s.d", cfg.Unit),
				art.ResolveName(d),
			)
			dropins[cfg.Unit] = append(files, p)
		}
		for u := range dalec.SortedMapIter(dropins) {
			dir := strings.Join([]string{
				`%dir`,
				filepath.Join(
					`%{_unitdir}`,
					fmt.Sprintf("%s.d", u),
				),
			}, " ")
			fmt.Fprintln(b, dir)

			for _, file := range dropins[u] {
				fmt.Fprintln(b, file)
			}
		}
	}

	for d, cfg := range dalec.SortedMapIter(artifacts.Docs) {
		path := filepath.Join(`%{_docdir}`, pkgName, cfg.SubPath, cfg.ResolveName(d))
		fullDirective := strings.Join([]string{`%doc`, path}, " ")
		fmt.Fprintln(b, fullDirective)
	}

	for l, cfg := range dalec.SortedMapIter(artifacts.Licenses) {
		path := filepath.Join(`%{_licensedir}`, pkgName, cfg.SubPath, cfg.ResolveName(l))
		fullDirective := strings.Join([]string{`%license`, path}, " ")
		fmt.Fprintln(b, fullDirective)
	}

	for l, cfg := range dalec.SortedMapIter(artifacts.Libs) {
		path := filepath.Join(`%{_libdir}`, cfg.SubPath, cfg.ResolveName(l))
		// Use %caps macro if capabilities are set and there's no chown
		capString := dalec.CapabilitiesString(cfg.LinuxCapabilities)
		if capString != "" && cfg.User == "" && cfg.Group == "" {
			fmt.Fprintf(b, "%%caps(%s) %s\n", capString, path)
		} else {
			fmt.Fprintln(b, path)
		}
	}

	for _, l := range artifacts.Links {
		user := l.User
		group := l.Group
		if user != "" || group != "" {
			if user == "" {
				user = "root"
			}
			if group == "" {
				group = "root"
			}
			fmt.Fprintf(b, "%%attr(-, %s, %s) %s\n", user, group, l.Dest)
		} else {
			fmt.Fprintln(b, l.Dest)
		}
	}

	for h, hf := range dalec.SortedMapIter(artifacts.Headers) {
		path := filepath.Join(`%{_includedir}`, hf.SubPath, hf.ResolveName(h))
		fmt.Fprintln(b, path)
	}
}

func (w *specWrapper) DisableStrip() string {
	if w.Spec.GetArtifacts(w.Target).DisableStrip {
		return "%global __strip /bin/true"
	}
	return ""
}

func (w *specWrapper) DisableAutoReq() string {
	artifacts := w.Spec.GetArtifacts(w.Target)
	if artifacts.DisableAutoRequires {
		return "AutoReq: no"
	}
	return ""
}

type subPkgWrapper struct {
	ResolvedName string
	Pkg          dalec.SubPackage
	ParentName   string
}

// RPM's -n form handles both derived and custom package names uniformly.
func (s *subPkgWrapper) directive() string {
	return "-n " + s.ResolvedName
}

func (w *specWrapper) SubPackages() (fmt.Stringer, error) {
	b := &strings.Builder{}

	for key, pkg := range dalec.GetSubPackagesForTarget(w.Spec, w.Target) {
		sw := &subPkgWrapper{
			ResolvedName: pkg.ResolvedName(w.Spec.Name, key),
			Pkg:          pkg,
			ParentName:   w.Spec.Name,
		}

		sw.writePackageHeader(b)
		sw.writeDescription(b)
		sw.writePost(b)
		sw.writePreUn(b)
		sw.writePostUn(b)
		sw.writeFiles(b)
	}

	return b, nil
}

func (s *subPkgWrapper) writePackageHeader(b *strings.Builder) {
	fmt.Fprintf(b, "%%package %s\n", s.directive())
	fmt.Fprintf(b, "Summary: %s\n", s.Pkg.Description)

	// Install-time requirements for the subpackage's own scriptlets. These do
	// not come from the spec's declared dependencies; they are derived from the
	// artifacts the subpackage ships (systemd units, users/groups, ownership).
	if s.Pkg.Artifacts != nil {
		if s.Pkg.Artifacts.DisableAutoRequires {
			b.WriteString("AutoReq: no\n")
		}
		b.WriteString(getSystemdRequires(s.Pkg.Artifacts.Systemd))
		b.WriteString(getUserPostRequires(s.Pkg.Artifacts.Users, s.Pkg.Artifacts.Groups))
		b.WriteString(getOwnershipPostRequires(*s.Pkg.Artifacts))
	}

	runtimeDeps := s.Pkg.Dependencies.GetRuntime()
	for name, constraints := range dalec.SortedMapIter(runtimeDeps) {
		writeDep(b, "Requires", name, constraints)
	}

	recommends := s.Pkg.Dependencies.GetRecommends()
	for name, constraints := range dalec.SortedMapIter(recommends) {
		writeDep(b, "Recommends", name, constraints)
	}

	for name, constraints := range dalec.SortedMapIter(s.Pkg.Provides) {
		writeDep(b, "Provides", name, constraints)
	}
	if s.Pkg.Artifacts != nil {
		writeUserGroupProvides(b, *s.Pkg.Artifacts)
	}

	for name, constraints := range dalec.SortedMapIter(s.Pkg.Conflicts) {
		writeDep(b, "Conflicts", name, constraints)
	}

	for name, constraints := range dalec.SortedMapIter(s.Pkg.Replaces) {
		writeDep(b, "Obsoletes", name, constraints)
	}

	b.WriteString("\n")
}

func (s *subPkgWrapper) writeDescription(b *strings.Builder) {
	fmt.Fprintf(b, "%%description %s\n", s.directive())
	fmt.Fprintf(b, "%s\n\n", s.Pkg.Description)
}

func (s *subPkgWrapper) writePost(b *strings.Builder) {
	if s.Pkg.Artifacts == nil {
		return
	}
	body := postScriptBody(*s.Pkg.Artifacts)
	if body == "" {
		return
	}
	fmt.Fprintf(b, "%%post %s\n", s.directive())
	b.WriteString(body)
	b.WriteString("\n")
}

func (s *subPkgWrapper) writePreUn(b *strings.Builder) {
	if s.Pkg.Artifacts == nil {
		return
	}
	body := preUnScriptBody(*s.Pkg.Artifacts)
	if body == "" {
		return
	}
	fmt.Fprintf(b, "%%preun %s\n", s.directive())
	b.WriteString(body)
	b.WriteString("\n")
}

func (s *subPkgWrapper) writePostUn(b *strings.Builder) {
	if s.Pkg.Artifacts == nil {
		return
	}
	body := postUnScriptBody(*s.Pkg.Artifacts)
	if body == "" {
		return
	}
	fmt.Fprintf(b, "%%postun %s\n", s.directive())
	b.WriteString(body)
	b.WriteString("\n")
}

func (s *subPkgWrapper) writeFiles(b *strings.Builder) {
	fmt.Fprintf(b, "%%files %s\n", s.directive())

	if s.Pkg.Artifacts != nil {
		writeFilesBody(b, *s.Pkg.Artifacts, s.ResolvedName)
	}
	b.WriteString("\n")
}

// WriteSpec generates an rpm spec from the provided [dalec.Spec] and distro target and writes it to the passed in writer
func WriteSpec(spec *dalec.Spec, target string, w io.Writer) error {
	return WriteSpecWithSourceFilter(spec, target, dalec.SourceFilterConfig{}, w)
}

func WriteSpecWithSourceFilter(spec *dalec.Spec, target string, filter dalec.SourceFilterConfig, w io.Writer) error {
	s := &specWrapper{Spec: spec, Target: target, SourceFilter: filter}

	err := specTmpl.Execute(w, s)
	if err != nil {
		return fmt.Errorf("error executing spec template: %w", err)
	}
	return nil
}
