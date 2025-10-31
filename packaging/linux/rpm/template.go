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
{{ .Changelog -}}
`)))

func optionalField(key, value string) string {
	if value == "" {
		return ""
	}
	return key + ": " + value + "\n"
}

var tmplFuncs = map[string]any{
	"optionalField": optionalField,
}

type specWrapper struct {
	*dalec.Spec
	Target string
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
	if len(provides) == 0 {
		return b
	}

	ls := dalec.SortMapKeys(provides)

	for _, name := range ls {
		writeDep(b, "Provides", name, provides[name])
	}
	b.WriteString("\n")
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

func (w *specWrapper) Requires() fmt.Stringer {
	b := &strings.Builder{}

	artifacts := w.Spec.GetArtifacts(w.Target)

	// first write post requires for systemd and user/group creation
	// as these do not come from dependencies in the spec
	// NOTE: This is a bit janky since different distributions may have different
	// package names... something to consider as we expand functionality.
	b.WriteString(getSystemdRequires(artifacts.Systemd))
	b.WriteString(getUserPostRequires(artifacts.Users, artifacts.Groups))

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
		fmt.Fprintf(b, "Source%d: %s\n", idx, ref)
	}

	sourceIdx := len(keys)

	if w.Spec.HasGomods() {
		fmt.Fprintf(b, "Source%d: %s.tar.gz\n", sourceIdx, gomodsName)
		sourceIdx += 1
	}

	if w.Spec.HasCargohomes() {
		fmt.Fprintf(b, "Source%d: %s.tar.gz\n", sourceIdx, cargohomeName)
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
	// if service isn't explicitly specified as enabled in the spec,
	// then we don't need to do anything in the preun script
	if !cfg.Enable {
		return ""
	}

	// should be equivalent to the systemd_preun scriptlet in the rpm spec,
	// but without the use of a .preset file
	return fmt.Sprintf(`
if [ $1 -eq 0 ]; then
    # complete uninstallation
    systemctl disable --now %s
fi
`, unitName)
}

func (w *specWrapper) PreUn() fmt.Stringer {
	b := &strings.Builder{}

	artifacts := w.GetArtifacts(w.Target)
	if artifacts.Systemd.IsEmpty() || (len(artifacts.Systemd.EnabledUnits()) == 0) {
		return b
	}

	b.WriteString("%preun\n")
	keys := dalec.SortMapKeys(artifacts.Systemd.Units)
	for _, servicePath := range keys {
		serviceName := filepath.Base(servicePath)
		unitConf := artifacts.Systemd.Units[servicePath]
		b.WriteString(
			systemdPreUnScript(serviceName, unitConf),
		)
	}
	return b
}

func systemdPostScript(unitName string, cfg dalec.SystemdUnitConfig) string {
	// if service isn't explicitly specified as enabled in the spec,
	// then we don't need to do anything in the post script
	if !cfg.Enable {
		return ""
	}

	// should be equivalent to the systemd_post scriptlet in the rpm spec,
	// but without the use of a .preset file
	s := `
if [ $1 -eq 1 ]; then
    # initial installation`

	// Enable/start service when package is installed
	if cfg.Start {
		s = s + fmt.Sprintf(`
    systemctl enable --now %s`, unitName)
	} else {
		s = s + fmt.Sprintf(`
    systemctl enable %s`, unitName)
	}

	s = s + `
fi
`

	return s
}

func (w *specWrapper) Post() fmt.Stringer {
	b := &strings.Builder{}

	systemd := w.postSystemd()
	users := w.postUsers()
	groups := w.postGroups()
	symlinkOwnership := w.getSymlinkOwnership()
	artifactOwnership := w.getArtifactOwnership()
	directoryOwnership := w.getDirectoryOwnership()

	if systemd == "" && users == "" && groups == "" && symlinkOwnership == "" && artifactOwnership == "" && directoryOwnership == "" {
		return b
	}

	b.WriteString("%post\n")
	if systemd != "" {
		b.WriteString(systemd)
	}
	if users != "" {
		b.WriteString(users)
	}
	if groups != "" {
		b.WriteString(groups)
	}
	if symlinkOwnership != "" {
		b.WriteString(symlinkOwnership)
	}
	if artifactOwnership != "" {
		b.WriteString(artifactOwnership)
	}
	if directoryOwnership != "" {
		b.WriteString(directoryOwnership)
	}

	b.WriteString("\n")
	return b
}

func (w *specWrapper) postUsers() string {
	artifacts := w.Spec.GetArtifacts(w.Target)
	if len(artifacts.Users) == 0 {
		return ""
	}

	b := &strings.Builder{}
	for _, user := range artifacts.Users {
		fmt.Fprintf(b, "getent passwd %s >/dev/null || adduser %s\n", user.Name, user.Name)
	}
	return b.String()
}

func (w *specWrapper) postGroups() string {
	artifacts := w.Spec.GetArtifacts(w.Target)
	if len(artifacts.Groups) == 0 {
		return ""
	}

	b := &strings.Builder{}
	for _, group := range artifacts.Groups {
		fmt.Fprintf(b, "getent group %s >/dev/null || groupadd --system %s\n", group.Name, group.Name)
	}
	return b.String()
}

func (w *specWrapper) getDirectoryOwnership() string {
	artifacts := w.Spec.GetArtifacts(w.Target)
	if artifacts.Directories == nil {
		return ""
	}
	b := &strings.Builder{}
	setDirOwnership := func(root, p string, cfg *dalec.ArtifactDirConfig) {
		if cfg == nil {
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
	configKeys := dalec.SortMapKeys(artifacts.Directories.Config)
	for _, p := range configKeys {
		cfg := artifacts.Directories.Config[p]
		setDirOwnership(`/%{_sysconfdir}`, p, &cfg)
	}
	stateKeys := dalec.SortMapKeys(artifacts.Directories.State)
	for _, p := range stateKeys {
		cfg := artifacts.Directories.State[p]
		setDirOwnership(`/%{_sharedstatedir}`, p, &cfg)
	}
	return b.String()
}

func (w *specWrapper) getArtifactOwnership() string {
	artifacts := w.Spec.GetArtifacts(w.Target)
	b := &strings.Builder{}

	setArtifactOwnership := func(root, p string, cfg *dalec.ArtifactConfig) {
		if cfg == nil {
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
		configKeys := dalec.SortMapKeys(artifacts.ConfigFiles)
		for _, c := range configKeys {
			cfg := artifacts.ConfigFiles[c]
			setArtifactOwnership(`/%{_sysconfdir}`, c, &cfg)
		}
	}
	if artifacts.DataDirs != nil {
		dataFileKeys := dalec.SortMapKeys(artifacts.DataDirs)
		for _, k := range dataFileKeys {
			df := artifacts.DataDirs[k]
			setArtifactOwnership(`/%{_datadir}`, k, &df)
		}
	}
	// Directory ownership is handled in getDirectoryOwnership; do not duplicate here.
	if artifacts.Libs != nil {
		libs := dalec.SortMapKeys(artifacts.Libs)
		for _, l := range libs {
			cfg := artifacts.Libs[l]
			setArtifactOwnership(`/%{_libdir}`, l, &cfg)
		}
	}
	if artifacts.Binaries != nil {
		binKeys := dalec.SortMapKeys(artifacts.Binaries)
		for _, p := range binKeys {
			cfg := artifacts.Binaries[p]
			setArtifactOwnership(`/%{_bindir}`, p, &cfg)
		}
	}

	return b.String()
}

func (w *specWrapper) getSymlinkOwnership() string {
	artifacts := w.Spec.GetArtifacts(w.Target)
	if len(artifacts.Links) == 0 {
		return ""
	}
	b := &strings.Builder{}

	users := make(map[string]struct{}, len(artifacts.Users))
	groups := make(map[string]struct{}, len(artifacts.Groups))
	for _, user := range artifacts.Users {
		users[user.Name] = struct{}{}
	}
	for _, group := range artifacts.Groups {
		groups[group.Name] = struct{}{}
	}

	for _, link := range artifacts.Links {
		if _, ok := users[link.User]; ok {
			fmt.Fprintf(b, "chown -h %s %s\n", link.User, link.Dest)
		}
		if _, ok := groups[link.Group]; ok {
			fmt.Fprintf(b, "chgrp -h %s %s\n", link.Group, link.Dest)
		}
	}
	return b.String()
}

func (w *specWrapper) postSystemd() string {
	artifacts := w.Spec.GetArtifacts(w.Target)
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
	keys := dalec.SortMapKeys(enabledUnits)
	for _, servicePath := range keys {
		unitConf := artifacts.Systemd.Units[servicePath]
		artifact := unitConf.Artifact()
		b.WriteString(
			systemdPostScript(artifact.ResolveName(servicePath), unitConf),
		)
	}

	return b.String()
}

func (w *specWrapper) PostUn() fmt.Stringer {
	b := &strings.Builder{}

	artifacts := w.GetArtifacts(w.Target)
	if artifacts.Systemd.IsEmpty() {
		return b
	}

	b.WriteString("%postun\n")
	keys := dalec.SortMapKeys(artifacts.Systemd.Units)
	for _, servicePath := range keys {
		cfg := artifacts.Systemd.Units[servicePath]
		a := cfg.Artifact()
		serviceName := a.ResolveName(servicePath)
		fmt.Fprintf(b, "%%systemd_postun %s\n", serviceName)
	}

	return b
}

func (w *specWrapper) Install() fmt.Stringer {
	b := &strings.Builder{}
	fmt.Fprintln(b, "%install")

	artifacts := w.Spec.GetArtifacts(w.Target)

	// If package-specific files are defined for RPM, assume that make install
	// or similar commands have already placed files in their intended locations
	// during the build phase. In this case, we skip the individual artifact copying.
	if _, ok := artifacts.PackageFiles["rpm"]; ok {
		// Still handle users, groups, and symlinks since these are metadata operations
		// that wouldn't typically be handled by make install
		for _, l := range artifacts.Links {
			fmt.Fprintln(b, "mkdir -p", filepath.Dir(filepath.Join("%{buildroot}", l.Dest)))
			fmt.Fprintln(b, "ln -sf", l.Source, "%{buildroot}/"+l.Dest)
		}
		b.WriteString("\n")
		return b
	}

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

	if len(artifacts.Binaries) > 0 {
		binKeys := dalec.SortMapKeys(artifacts.Binaries)
		for _, p := range binKeys {
			cfg := artifacts.Binaries[p]
			copyArtifact(`%{buildroot}/%{_bindir}`, p, &cfg)
		}
	}

	if len(artifacts.Manpages) > 0 {
		manKeys := dalec.SortMapKeys(artifacts.Manpages)
		for _, p := range manKeys {
			cfg := artifacts.Manpages[p]
			copyArtifact(`%{buildroot}/%{_mandir}`, p, &cfg)
		}
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
		configKeys := dalec.SortMapKeys(artifacts.Directories.Config)
		for _, p := range configKeys {
			cfg := artifacts.Directories.Config[p]
			createArtifactDir(`%{buildroot}/%{_sysconfdir}`, p, cfg)
		}

		stateKeys := dalec.SortMapKeys(artifacts.Directories.State)
		for _, p := range stateKeys {
			cfg := artifacts.Directories.State[p]
			createArtifactDir(`%{buildroot}/%{_sharedstatedir}`, p, cfg)
		}
	}

	if len(artifacts.DataDirs) > 0 {
		dataFileKeys := dalec.SortMapKeys(artifacts.DataDirs)
		for _, k := range dataFileKeys {
			df := artifacts.DataDirs[k]
			copyArtifact(`%{buildroot}/%{_datadir}`, k, &df)
		}
	}

	if artifacts.Libexec != nil {
		libexecFileKeys := dalec.SortMapKeys(artifacts.Libexec)
		for _, k := range libexecFileKeys {
			le := artifacts.Libexec[k]
			copyArtifact(`%{buildroot}/%{_libexecdir}`, k, &le)
		}
	}

	configKeys := dalec.SortMapKeys(artifacts.ConfigFiles)
	for _, c := range configKeys {
		cfg := artifacts.ConfigFiles[c]
		copyArtifact(`%{buildroot}/%{_sysconfdir}`, c, &cfg)
	}

	if artifacts.Systemd != nil {
		serviceKeys := dalec.SortMapKeys(artifacts.Systemd.Units)
		for _, p := range serviceKeys {
			cfg := artifacts.Systemd.Units[p]
			// must include systemd unit extension (.service, .socket, .timer, etc.) in name
			copyArtifact(`%{buildroot}/%{_unitdir}`, p, cfg.Artifact())
		}

		dropinKeys := dalec.SortMapKeys(artifacts.Systemd.Dropins)
		for _, d := range dropinKeys {
			cfg := artifacts.Systemd.Dropins[d]
			copyArtifact(`%{buildroot}/%{_unitdir}`, d, cfg.Artifact())
		}
	}

	docKeys := dalec.SortMapKeys(artifacts.Docs)
	for _, d := range docKeys {
		cfg := artifacts.Docs[d]
		root := filepath.Join(`%{buildroot}/%{_docdir}`, w.Name)
		copyArtifact(root, d, &cfg)
	}

	licenseKeys := dalec.SortMapKeys(artifacts.Licenses)
	for _, l := range licenseKeys {
		cfg := artifacts.Licenses[l]
		root := filepath.Join(`%{buildroot}/%{_licensedir}`, w.Name)
		copyArtifact(root, l, &cfg)
	}

	libs := dalec.SortMapKeys(artifacts.Libs)
	for _, l := range libs {
		cfg := artifacts.Libs[l]
		root := filepath.Join(`%{buildroot}/%{_libdir}`)
		copyArtifact(root, l, &cfg)
	}

	for _, l := range artifacts.Links {
		fmt.Fprintln(b, "mkdir -p", filepath.Dir(filepath.Join("%{buildroot}", l.Dest)))
		fmt.Fprintln(b, "ln -sf", l.Source, "%{buildroot}/"+l.Dest)
	}

	headersKeys := dalec.SortMapKeys(artifacts.Headers)
	for _, h := range headersKeys {
		cfg := artifacts.Headers[h]
		copyArtifact(`%{buildroot}/%{_includedir}`, h, &cfg)
	}
	b.WriteString("\n")
	return b
}

func (w *specWrapper) Files() fmt.Stringer {
	b := &strings.Builder{}
	fmt.Fprintf(b, "%%files\n")

	artifacts := w.GetArtifacts(w.Target)

	// If package-specific files are defined for RPM, use them instead of generating 
	// the file list from individual artifact categories
	if rpmFiles, ok := artifacts.PackageFiles["rpm"]; ok {
		fmt.Fprint(b, rpmFiles)
		if !strings.HasSuffix(rpmFiles, "\n") {
			fmt.Fprintln(b)
		}
		fmt.Fprintln(b)
		return b
	}

	if len(artifacts.Binaries) > 0 {
		binKeys := dalec.SortMapKeys(artifacts.Binaries)
		for _, p := range binKeys {
			cfg := artifacts.Binaries[p]
			full := filepath.Join(`%{_bindir}/`, cfg.SubPath, cfg.ResolveName(p))
			fmt.Fprintln(b, full)
		}
	}

	if len(artifacts.Manpages) > 0 {
		fmt.Fprintln(b, `%{_mandir}/*/*`)
	}

	if artifacts.Directories != nil {
		configKeys := dalec.SortMapKeys(artifacts.Directories.Config)
		for _, p := range configKeys {
			dir := strings.Join([]string{`%dir`, filepath.Join(`%{_sysconfdir}`, p)}, " ")
			fmt.Fprintln(b, dir)
		}

		stateKeys := dalec.SortMapKeys(artifacts.Directories.State)
		for _, p := range stateKeys {
			dir := strings.Join([]string{`%dir`, filepath.Join(`%{_sharedstatedir}`, p)}, " ")
			fmt.Fprintln(b, dir)
		}
	}

	if artifacts.DataDirs != nil {
		dataKeys := dalec.SortMapKeys(artifacts.DataDirs)
		for _, k := range dataKeys {
			df := artifacts.DataDirs[k]
			fullPath := filepath.Join(`%{_datadir}`, df.SubPath, df.ResolveName(k))
			fmt.Fprintln(b, fullPath)
		}
	}

	if artifacts.Libexec != nil {
		dataKeys := dalec.SortMapKeys(artifacts.Libexec)
		for _, k := range dataKeys {
			le := artifacts.Libexec[k]
			targetDir := filepath.Join(`%{_libexecdir}`, le.SubPath)
			fullPath := filepath.Join(targetDir, le.ResolveName(k))
			fmt.Fprintln(b, fullPath)
		}
	}

	configKeys := dalec.SortMapKeys(artifacts.ConfigFiles)
	for _, c := range configKeys {
		cfg := artifacts.ConfigFiles[c]
		fullPath := filepath.Join(`%{_sysconfdir}`, cfg.SubPath, cfg.ResolveName(c))
		fullDirective := strings.Join([]string{`%config(noreplace)`, fullPath}, " ")
		fmt.Fprintln(b, fullDirective)
	}

	if artifacts.Systemd != nil {
		serviceKeys := dalec.SortMapKeys(artifacts.Systemd.Units)
		for _, p := range serviceKeys {
			cfg := artifacts.Systemd.Units[p]
			a := cfg.Artifact()
			unitPath := filepath.Join(`%{_unitdir}/`, a.SubPath, a.ResolveName(p))
			fmt.Fprintln(b, unitPath)
		}

		dropins := make(map[string][]string)
		// process these to get a unique list of files per unit name.
		// we need a single dir entry for the directory
		// need a file entry for each of files
		dropinKeys := dalec.SortMapKeys(artifacts.Systemd.Dropins)
		for _, d := range dropinKeys {
			cfg := artifacts.Systemd.Dropins[d]
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
		unitNames := dalec.SortMapKeys(dropins)
		for _, u := range unitNames {
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

	docKeys := dalec.SortMapKeys(artifacts.Docs)
	for _, d := range docKeys {
		cfg := artifacts.Docs[d]
		path := filepath.Join(`%{_docdir}`, w.Name, cfg.SubPath, cfg.ResolveName(d))
		fullDirective := strings.Join([]string{`%doc`, path}, " ")
		fmt.Fprintln(b, fullDirective)
	}

	licenseKeys := dalec.SortMapKeys(artifacts.Licenses)
	for _, l := range licenseKeys {
		cfg := artifacts.Licenses[l]
		path := filepath.Join(`%{_licensedir}`, w.Name, cfg.SubPath, cfg.ResolveName(l))
		fullDirective := strings.Join([]string{`%license`, path}, " ")
		fmt.Fprintln(b, fullDirective)
	}

	libKeys := dalec.SortMapKeys(artifacts.Libs)
	for _, l := range libKeys {
		cfg := artifacts.Libs[l]
		path := filepath.Join(`%{_libdir}`, cfg.SubPath, cfg.ResolveName(l))
		fmt.Fprintln(b, path)
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

	if len(artifacts.Headers) > 0 {
		headersKeys := dalec.SortMapKeys(artifacts.Headers)
		for _, h := range headersKeys {
			hf := artifacts.Headers[h]
			path := filepath.Join(`%{_includedir}`, hf.SubPath, hf.ResolveName(h))
			fmt.Fprintln(b, path)
		}
	}
	b.WriteString("\n")
	return b
}

func (w *specWrapper) DisableStrip() string {
	artifacts := w.Spec.GetArtifacts(w.Target)
	if artifacts.DisableStrip {
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

// WriteSpec generates an rpm spec from the provided [dalec.Spec] and distro target and writes it to the passed in writer
func WriteSpec(spec *dalec.Spec, target string, w io.Writer) error {
	s := &specWrapper{spec, target}

	err := specTmpl.Execute(w, s)
	if err != nil {
		return fmt.Errorf("error executing spec template: %w", err)
	}
	return nil
}
