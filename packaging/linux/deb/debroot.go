package deb

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"

	_ "embed"

	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend/pkg/bkfs"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/pkg/errors"
)

const (
	DebHelperCompat           = "11"
	customSystemdPostinstFile = "custom_systemd_postinst.sh.partial"
	BinariesPath              = "/usr/bin"
	ConfigFilesPath           = "/etc"
	ManpagesPath              = "/usr/share/doc/manpages"
	HeadersPath               = "/usr/include"
	LicensesPath              = "/usr/share/doc"
	DocsPath                  = "/usr/share/doc"
	LibsPath                  = "/usr/lib"
	LibexecPath               = "/usr/libexec"
	DataDirsPath              = "/usr/share"
)

//go:embed templates/patch-header.txt
var patchHeader []byte

//go:embed templates/debian_install_header.sh
var debianInstall []byte

// This creates a directory in the debian root directory for each patch, and copies the patch files into it.
// The format for each patch dir matches what would normally be under `debian/patches`, just that this is a separate dir for every source we are patching
// This is purely for documenting in the source package how patches are applied in a more readable way than the big merged patch file.
func sourcePatchesDir(sOpt dalec.SourceOpts, base llb.State, dir, name string, spec *dalec.Spec, opts ...llb.ConstraintsOpt) []llb.State {
	patchesPath := filepath.Join(dir, name)
	base = base.
		File(llb.Mkdir(patchesPath, 0o755), opts...)

	var states []llb.State

	seriesBuf := bytes.NewBuffer(nil)
	for _, patch := range spec.Patches[name] {
		src := spec.Sources[patch.Source]

		copySrc := patch.Source
		if patch.Path != "" {
			src.Includes = append(src.Includes, patch.Path)
			copySrc = patch.Path
		}

		patchStateName := ""
		if !src.IsDir() {
			patchStateName = patch.Source
		}
		st := src.ToState(patchStateName, sOpt, opts...)

		st = base.File(llb.Copy(st, copySrc, filepath.Join(patchesPath, patch.Source)), opts...)
		seriesBuf.WriteString(name + "\n")
		states = append(states, st)
	}

	series := base.File(llb.Mkfile(filepath.Join(patchesPath, "series"), 0o640, seriesBuf.Bytes()), opts...)

	return append(states, series)
}

type SourcePkgConfig struct {
	// PrependPath is a list of paths to be prepended to the $PATH var in build
	// scripts.
	PrependPath []string
	// AppendPath is a list of paths to be appended to the $PATH var in build
	// scripts.
	AppendPath []string
}

// Addpath creates a SourcePkgConfig where the first argument is sets
// [SourcePkgConfig.PrependPath] and the 2nd argument sets
// [SourcePkgConfig.AppendPath]
func AddPath(pre, post []string) SourcePkgConfig {
	return SourcePkgConfig{
		PrependPath: pre,
		AppendPath:  post,
	}
}

// Debroot creates a debian root directory suitable for use with debbuild.
// This does not include sources in case you want to mount sources (instead of copying them) later.
//
// Set the `distroVersionID` argument to a value suitable for including in the
// .deb for storing the targeted distro+version in the deb.
// This is generally needed so that if a distro user upgrades from, for instance,
// debian 11 to debian 12, that the package built for debian 12 will be considered
// an upgrade even if it is technically the same underlying source.
// It may be left blank but is highly recommended to set this.
// Use [ReadDistroVersionID] to get a suitable value.
func Debroot(ctx context.Context, sOpt dalec.SourceOpts, spec *dalec.Spec, worker, in llb.State, target, dir, distroVersionID string, cfg SourcePkgConfig, opts ...llb.ConstraintsOpt) (llb.State, error) {
	control, err := controlFile(spec, in, target, dir)
	if err != nil {
		return llb.Scratch(), errors.Wrap(err, "error generating control file")
	}

	rules, err := Rules(spec, in, dir, target)
	if err != nil {
		return llb.Scratch(), errors.Wrap(err, "error generating rules file")
	}

	changelog, err := Changelog(spec, in, target, dir, distroVersionID)
	if err != nil {
		return llb.Scratch(), errors.Wrap(err, "error generating changelog file")
	}

	// Generate README.Debian with source provenance documentation
	readmeDebian := generateReadmeDebian(spec)
	var readmeDebianState llb.State
	if readmeDebian != nil {
		readmeDebianState = in.
			File(llb.Mkdir(filepath.Join(dir), 0o755, llb.WithParents(true))).
			File(llb.Mkfile(filepath.Join(dir, "README.Debian"), 0o644, readmeDebian))
	} else {
		readmeDebianState = in.File(llb.Mkdir(filepath.Join(dir), 0o755, llb.WithParents(true)))
	}

	if dir == "" {
		dir = "debian"
	}

	base := llb.Scratch().File(llb.Mkdir(dir, 0o755), opts...)
	installers := createInstallScripts(worker, spec, dir, target)

	const (
		sourceFormat = "3.0 (quilt)"
	)

	debian := base.
		File(llb.Mkdir(filepath.Join(dir, "source"), 0o755), opts...).
		File(llb.Mkfile(filepath.Join(dir, "source/format"), 0o640, []byte(sourceFormat)), opts...).
		File(llb.Mkdir(filepath.Join(dir, "dalec"), 0o755), opts...).
		File(llb.Mkfile(filepath.Join(dir, "source/include-binaries"), 0o640, append([]byte("dalec"), '\n')), opts...)

	states := []llb.State{control, rules, changelog, readmeDebianState, debian}
	states = append(states, installers...)

	dalecDir := base.
		File(llb.Mkdir(filepath.Join(dir, "dalec"), 0o755), opts...)

	states = append(states, dalecDir.File(llb.Mkfile(filepath.Join(dir, "dalec/build.sh"), 0o700, createBuildScript(spec, &cfg)), opts...))
	states = append(states, dalecDir.File(llb.Mkfile(filepath.Join(dir, "dalec/patch.sh"), 0o700, createPatchScript(spec, &cfg)), opts...))
	states = append(states, dalecDir.File(llb.Mkfile(filepath.Join(dir, "dalec/fix_generators.sh"), 0o700, fixupGenerators(spec, &cfg)), opts...))
	states = append(states, dalecDir.File(llb.Mkfile(filepath.Join(dir, "dalec/fix_perms.sh"), 0o700, fixupArtifactPerms(spec, target, &cfg)), opts...))

	customEnable, err := customDHInstallSystemdPostinst(spec, target)
	if err != nil {
		return llb.Scratch(), err
	}
	if len(customEnable) > 0 {
		// This is not meant to be executed on its own and will instead get added
		// to a post inst file, so need to mark this as executable.
		states = append(states, dalecDir.File(llb.Mkfile(filepath.Join(dir, "dalec/"+customSystemdPostinstFile), 0o600, customEnable), opts...))
	}

	postinst := bytes.NewBuffer(nil)
	artifacts := spec.GetArtifacts(target)
	writeUsersPostInst(postinst, artifacts.Users)
	writeGroupsPostInst(postinst, artifacts.Groups)
	setArtifactOwnershipPostInst(postinst, spec, target)

	if postinst.Len() > 0 {
		dt := []byte("#!/usr/bin/env sh\nset -e\n")
		dt = append(dt, postinst.Bytes()...)

		states = append(states, dalecDir.File(llb.Mkfile(filepath.Join(dir, "postinst"), 0o700, dt), opts...))
	}

	patchDir := dalecDir.File(llb.Mkdir(filepath.Join(dir, "dalec/patches"), 0o755), opts...)
	sorted := dalec.SortMapKeys(spec.Patches)
	for _, name := range sorted {
		pls := sourcePatchesDir(sOpt, patchDir, filepath.Join(dir, "dalec/patches"), name, spec, opts...)
		states = append(states, pls...)
	}

	if len(artifacts.Links) > 0 {
		buf := bytes.NewBuffer(nil)

		for _, l := range artifacts.Links {
			src := strings.TrimPrefix(l.Source, "/")
			dst := strings.TrimPrefix(l.Dest, "/")
			fmt.Fprintln(buf, src, dst)
		}

		states = append(states, dalecDir.File(llb.Mkfile(filepath.Join(dir, spec.Name+".links"), 0o644, buf.Bytes()), opts...))
	}

	return dalec.MergeAtPath(in, states, "/"), nil
}

func fixupArtifactPerms(spec *dalec.Spec, target string, cfg *SourcePkgConfig) []byte {
	buf := bytes.NewBuffer(nil)
	writeScriptHeader(buf, cfg)

	basePath := filepath.Join("debian", spec.Name)
	artifacts := spec.GetArtifacts(target)

	checkAndWritePerms := func(artifacts map[string]dalec.ArtifactConfig, dir string) {
		if artifacts == nil {
			return
		}
		sorted := dalec.SortMapKeys(artifacts)
		for _, key := range sorted {
			cfg := artifacts[key]
			resolvedName := cfg.ResolveName(key)
			p := filepath.Join(basePath, dir, resolvedName)
			// TODO: do i need this?
			if cfg.SubPath != "" {
				p = filepath.Join(basePath, dir, cfg.SubPath, resolvedName)
			}

			if cfg.Permissions.Perm() != 0 {
				fmt.Fprintf(buf, "chmod %o %q\n", cfg.Permissions.Perm(), p)
				continue
			}

			// Debian does not keep original permissions for files, so we check if artifact matches a source name
			// and if so, we apply the source permissions for inline sources.
			srcKey, subpath, _ := strings.Cut(key, "/")
			src, ok := spec.Sources[srcKey]
			if !ok || src.Inline == nil {
				continue
			}

			if src.Inline.File != nil && src.Inline.File.Permissions.Perm() != 0 {
				fmt.Fprintf(buf, "chmod %o %q\n", src.Inline.File.Permissions.Perm(), p)
				continue
			}

			if src.Inline.Dir == nil {
				continue
			}
			if subpath == "" {
				if src.Inline.Dir.Permissions.Perm() != 0 {
					fmt.Fprintf(buf, "chmod %o %q\n", src.Inline.Dir.Permissions.Perm(), p)
				}
				continue
			}
			if f, ok := src.Inline.Dir.Files[subpath]; ok && f.Permissions.Perm() != 0 {
				fmt.Fprintf(buf, "chmod %o %q\n", f.Permissions.Perm(), p)
			}
		}
	}

	checkAndWritePerms(artifacts.Binaries, BinariesPath)
	checkAndWritePerms(artifacts.ConfigFiles, ConfigFilesPath)
	checkAndWritePerms(artifacts.Manpages, filepath.Join(ManpagesPath, spec.Name))
	checkAndWritePerms(artifacts.Headers, HeadersPath)
	checkAndWritePerms(artifacts.Licenses, filepath.Join(LicensesPath, spec.Name))
	checkAndWritePerms(artifacts.Docs, filepath.Join(DocsPath, spec.Name))
	checkAndWritePerms(artifacts.Libs, LibsPath)
	checkAndWritePerms(artifacts.Libexec, LibexecPath)
	checkAndWritePerms(artifacts.DataDirs, DataDirsPath)

	if artifacts.Directories != nil {
		sorted := dalec.SortMapKeys(artifacts.Directories.GetConfig())
		for _, name := range sorted {
			cfg := artifacts.Directories.Config[name]
			if cfg.Mode.Perm() != 0 {
				p := filepath.Join(basePath, "/etc", name)
				fmt.Fprintf(buf, "chmod %o %q\n", cfg.Mode.Perm(), p)
			}
		}

		sorted = dalec.SortMapKeys(artifacts.Directories.GetState())
		for _, name := range sorted {
			cfg := artifacts.Directories.State[name]
			if cfg.Mode.Perm() != 0 {
				p := filepath.Join(basePath, "/var/lib", name)
				fmt.Fprintf(buf, "chmod %o %q\n", cfg.Mode.Perm(), p)
			}
		}
	}

	return buf.Bytes()
}

// For debian sources
// This is called from `debian/rules` after the source tarball has been extracted.
func fixupGenerators(spec *dalec.Spec, cfg *SourcePkgConfig) []byte {
	buf := bytes.NewBuffer(nil)
	writeScriptHeader(buf, cfg)
	if spec.HasGomods() {
		// Older go versions did not have support for the `GOMODCACHE` var
		// This is a hack to try and make the build work by linking the go modules
		// we've already fetched into to module dir under $GOPATH
		// The default GOMODCACHE value is ${GOPATH}/pkg/mod.
		fmt.Fprintf(buf, `test -n "$(go env GOMODCACHE)" || (GOPATH="$(go env GOPATH)"; mkdir -p "${GOPATH}/pkg" && ln -s "$(pwd)/%s" "${GOPATH}/pkg/mod")`, gomodsName)
		// Above command does not have a newline due to quoting issues, so add that here.
		fmt.Fprint(buf, "\n")
	}

	return buf.Bytes()
}

func setupPathVar(pre, post []string) string {
	if len(pre) == 0 && len(post) == 0 {
		return ""
	}

	full := append(pre, "$PATH")
	full = append(full, post...)
	return strings.Join(full, ":")
}

func writeScriptHeader(buf *bytes.Buffer, cfg *SourcePkgConfig) {
	fmt.Fprintln(buf, "#!/usr/bin/env sh")
	fmt.Fprintln(buf)

	fmt.Fprintln(buf, "set -ex")

	if cfg != nil {
		if pathVar := setupPathVar(cfg.PrependPath, cfg.AppendPath); pathVar != "" {
			fmt.Fprintln(buf, "export PATH="+pathVar)
		}
	}
}

func createPatchScript(spec *dalec.Spec, cfg *SourcePkgConfig) []byte {
	buf := bytes.NewBuffer(nil)

	writeScriptHeader(buf, cfg)

	for name, patches := range spec.Patches {
		for _, patch := range patches {
			p := filepath.Join("${DEBIAN_DIR:=debian}/dalec/patches", name, patch.Source)
			fmt.Fprintf(buf, "patch -d %q -p%d -s < %q\n", name, *patch.Strip, p)
		}
	}

	return buf.Bytes()
}

func createBuildScript(spec *dalec.Spec, cfg *SourcePkgConfig) []byte {
	buf := bytes.NewBuffer(nil)
	writeScriptHeader(buf, cfg)

	if spec.HasPips() {
		// Set up pip environment and install dependencies during build
		fmt.Fprintln(buf, "# Set up pip environment")
		fmt.Fprintf(buf, "export PIP_CACHE_DIR=\"$(pwd)/%s\"\n", pipDepsName)
		fmt.Fprintln(buf, "")
		fmt.Fprintln(buf, "# Install pip dependencies from cache")
		fmt.Fprintln(buf, "for reqfile in $(find . -name 'requirements*.txt' -o -name 'pyproject.toml' -o -name 'setup.py'); do")
		fmt.Fprintln(buf, "  if [ -f \"$reqfile\" ]; then")
		fmt.Fprintln(buf, "    reqdir=$(dirname \"$reqfile\")")
		fmt.Fprintln(buf, "    mkdir -p \"$reqdir/site-packages\"")
		fmt.Fprintln(buf, "    case \"$reqfile\" in")
		fmt.Fprintln(buf, "      *.txt) python3 -m pip install --target=\"$reqdir/site-packages\" --find-links=\"${PIP_CACHE_DIR}\" --no-index --requirement=\"$reqfile\" 2>/dev/null || python3 -m pip install --target=\"$reqdir/site-packages\" --find-links=\"${PIP_CACHE_DIR}\" --no-index --requirement=\"$reqfile\" ;;")
		fmt.Fprintln(buf, "      *) (cd \"$reqdir\" && python3 -m pip install --target=site-packages --find-links=\"${PIP_CACHE_DIR}\" --no-index . 2>/dev/null || python3 -m pip install --target=site-packages --find-links=\"${PIP_CACHE_DIR}\" --no-index .) ;;")
		fmt.Fprintln(buf, "    esac")
		fmt.Fprintln(buf, "    export PYTHONPATH=\"$reqdir/site-packages:${PYTHONPATH}\"")
		fmt.Fprintln(buf, "  fi")
		fmt.Fprintln(buf, "done")
		fmt.Fprintln(buf, "")
	}

	sorted := dalec.SortMapKeys(spec.Build.Env)
	for _, k := range sorted {
		v := spec.Build.Env[k]
		fmt.Fprintf(buf, "export %q=%q\n", k, v)
	}

	for _, step := range spec.Build.Steps {
		fmt.Fprintln(buf)
		fmt.Fprintln(buf, "(")

		sorted := dalec.SortMapKeys(step.Env)
		for _, k := range sorted {
			v := step.Env[k]
			fmt.Fprintf(buf, "	export %q=%q\n", k, v)
		}

		fmt.Fprintln(buf, step.Command)
		fmt.Fprintln(buf, ")")
	}

	return buf.Bytes()
}

func createInstallScripts(worker llb.State, spec *dalec.Spec, dir, target string) []llb.State {
	artifacts := spec.GetArtifacts(target)

	states := make([]llb.State, 1)
	base := llb.Scratch().File(llb.Mkdir(dir, 0o755, llb.WithParents(true)))

	installBuf := bytes.NewBuffer(nil)
	writeInstallHeader := sync.OnceFunc(func() {
		fmt.Fprintln(installBuf, string(debianInstall))
	})

	writeInstall := func(src, dir, name string) {
		// This is wrapped in a sync.OnceFunc so that this only has an effect the
		// first time it is called.
		writeInstallHeader()

		name = strings.TrimSuffix(name, "*")
		dest := filepath.Join("debian", spec.Name, dir, name)
		fmt.Fprintln(installBuf, "do_install", filepath.Dir(dest), dest, src)

	}

	if len(artifacts.Binaries) > 0 {
		sorted := dalec.SortMapKeys(artifacts.Binaries)
		for _, key := range sorted {
			cfg := artifacts.Binaries[key]
			writeInstall(key, filepath.Join(BinariesPath, cfg.SubPath), cfg.ResolveName(key))
		}
	}

	if len(artifacts.ConfigFiles) > 0 {
		sorted := dalec.SortMapKeys(artifacts.ConfigFiles)
		for _, p := range sorted {
			cfg := artifacts.ConfigFiles[p]

			dir := filepath.Join(ConfigFilesPath, cfg.SubPath)
			name := cfg.ResolveName(p)
			writeInstall(p, dir, name)
		}
	}

	if len(artifacts.Manpages) > 0 {
		buf := bytes.NewBuffer(nil)

		sorted := dalec.SortMapKeys(artifacts.Manpages)
		for _, key := range sorted {
			cfg := artifacts.Manpages[key]
			if cfg.Name != "" || (cfg.SubPath != "" && cfg.SubPath != filepath.Base(filepath.Dir(key))) {
				resolved := cfg.ResolveName(key)
				writeInstall(key, filepath.Join(ManpagesPath, spec.Name, cfg.SubPath), resolved)
				continue
			}
			fmt.Fprintln(buf, key)
		}
		if buf.Len() > 0 {
			states = append(states, base.File(llb.Mkfile(filepath.Join(dir, spec.Name+".manpages"), 0o640, buf.Bytes())))
		}

	}

	if artifacts.Directories != nil {
		buf := bytes.NewBuffer(nil)

		sorted := dalec.SortMapKeys(artifacts.Directories.Config)
		for _, name := range sorted {
			fmt.Fprintln(buf, filepath.Join("/etc", name))
		}

		sorted = dalec.SortMapKeys(artifacts.Directories.State)
		for _, name := range sorted {
			fmt.Fprintln(buf, filepath.Join("/var/lib", name))
		}

		states = append(states, base.File(llb.Mkfile(filepath.Join(dir, spec.Name+".dirs"), 0o640, buf.Bytes())))
	}

	if len(artifacts.Docs) > 0 || len(artifacts.Licenses) > 0 {
		buf := bytes.NewBuffer(nil)

		sorted := dalec.SortMapKeys(artifacts.Docs)
		for _, key := range sorted {
			cfg := artifacts.Docs[key]
			resolved := cfg.ResolveName(key)
			if resolved != key || cfg.SubPath != "" {
				writeInstall(key, filepath.Join(DocsPath, spec.Name, cfg.SubPath), resolved)
			} else {
				fmt.Fprintln(buf, key)
			}
		}

		sorted = dalec.SortMapKeys(artifacts.Licenses)
		for _, key := range sorted {
			cfg := artifacts.Licenses[key]
			resolved := cfg.ResolveName(key)
			if resolved != key || cfg.SubPath != "" {
				writeInstall(key, filepath.Join(LicensesPath, spec.Name, cfg.SubPath), resolved)
			} else {
				fmt.Fprintln(buf, key)
			}
		}

		if buf.Len() > 0 {
			states = append(states, base.File(llb.Mkfile(filepath.Join(dir, spec.Name+".docs"), 0o640, buf.Bytes())))
		}
	}

	if len(artifacts.Headers) > 0 {
		sorted := dalec.SortMapKeys(artifacts.Headers)
		for _, key := range sorted {
			cfg := artifacts.Headers[key]
			resolved := cfg.ResolveName(key)
			writeInstall(key, filepath.Join(HeadersPath, cfg.SubPath), resolved)
		}
	}

	if units := artifacts.Systemd.GetUnits(); len(units) > 0 {
		// deb-systemd will look for service files in DEBIAN/<package-name>[.<service-name>].<unit-type>
		// To handle this we'll create symlinks to the actual unit files in the source.
		// https://manpages.debian.org/testing/debhelper/dh_installsystemd.1.en.html#FILES

		// Maps the base name of a unit, e.g. "foo.service" -> foo, to the list of
		// units that fall under that basename
		// (e.g. "foo.socket" and  "foo.service")
		// We need to track this in cases where some units under a base are
		// enabled and some are not since dh_installsystemd does not support this
		// directly.

		sorted := dalec.SortMapKeys(units)
		for _, key := range sorted {
			cfg := units[key]
			name, suffix := cfg.SplitName(key)
			if name != spec.Name {
				name = spec.Name + "." + name
			}

			name = name + "." + suffix

			// Unforutnately there is not currently any way to create a symlink
			// directory with llb, so we need to use the worker to create the
			// symlink for us.
			st := worker.Run(
				llb.Dir(filepath.Join("/tmp/work", dir)),
				dalec.ShArgs("ln -s ../"+key+" "+name),
			).AddMount("/tmp/work", llb.Scratch())

			states = append(states, st)
		}
	}

	if dropins := artifacts.Systemd.GetDropins(); len(dropins) > 0 {
		sorted := dalec.SortMapKeys(dropins)
		for _, key := range sorted {
			cfg := dropins[key]
			cfgA := cfg.Artifact()
			name := cfgA.ResolveName(key)

			writeInstall(key, filepath.Join("/lib/systemd/system", cfg.Unit+".d"), name)
		}
	}

	if len(artifacts.DataDirs) > 0 {
		sorted := dalec.SortMapKeys(artifacts.DataDirs)
		for _, key := range sorted {
			cfg := artifacts.DataDirs[key]
			resolved := cfg.ResolveName(key)
			writeInstall(key, filepath.Join(DataDirsPath, cfg.SubPath), resolved)
		}
	}

	if len(artifacts.Libexec) > 0 {
		sorted := dalec.SortMapKeys(artifacts.Libexec)
		for _, key := range sorted {
			cfg := artifacts.Libexec[key]
			resolved := cfg.ResolveName(key)
			targetDir := filepath.Join(LibexecPath, cfg.SubPath)
			writeInstall(key, targetDir, resolved)
		}
	}

	if len(artifacts.Libs) > 0 {
		sorted := dalec.SortMapKeys(artifacts.Libs)
		for _, key := range sorted {
			cfg := artifacts.Libs[key]
			resolved := cfg.ResolveName(key)
			writeInstall(key, filepath.Join(LibsPath, cfg.SubPath), resolved)
		}
	}

	if installBuf.Len() > 0 {
		states = append(states, base.File(llb.Mkfile(filepath.Join(dir, spec.Name+".install"), 0o700, installBuf.Bytes())))
	}

	return states
}

func controlFile(spec *dalec.Spec, in llb.State, target, dir string) (llb.State, error) {
	buf := bytes.NewBuffer(nil)
	info, _ := debug.ReadBuildInfo()
	buf.WriteString("# Automatically generated by " + info.Main.Path + "\n")
	buf.WriteString("\n")

	if dir == "" {
		dir = "debian"
	}

	if err := WriteControl(spec, target, buf); err != nil {
		return llb.Scratch(), err
	}

	return in.
			File(llb.Mkdir(dir, 0o755, llb.WithParents(true))).
			File(llb.Mkfile(filepath.Join(dir, "control"), 0o640, buf.Bytes())),
		nil
}

// ReadDistroVersionID returns a string concatenating the values of ID and
// VERSION_ID from /etc/os-release from the provided state.
func ReadDistroVersionID(ctx context.Context, client gwclient.Client, st llb.State) (string, error) {
	rootfs, err := bkfs.FromState(ctx, &st, client)
	if err != nil {
		return "", err
	}

	f, err := rootfs.Open("etc/os-release")
	if err != nil {
		return "", err
	}

	defer f.Close()

	scanner := bufio.NewScanner(f)
	var (
		id      string
		version string
	)

	for scanner.Scan() {
		k, v, ok := strings.Cut(scanner.Text(), "=")
		if !ok {
			continue
		}
		switch k {
		case "ID":
			id = unquote(v)
		case "VERSION_ID":
			version = unquote(v)
		}

		if id != "" && version != "" {
			break
		}
	}

	if scanner.Err() != nil {
		return "", err
	}

	if id == "" || version == "" {
		return "", errors.New("could not determine distro or version ID")
	}

	return id + version, nil
}

func unquote(v string) string {
	if updated, err := strconv.Unquote(v); err == nil {
		return updated
	}
	return v
}

func writeUsersPostInst(w *bytes.Buffer, users []dalec.AddUserConfig) {
	for _, u := range users {
		fmt.Fprintf(w, "getent passwd %s >/dev/null || useradd %s\n", u.Name, u.Name)
	}
}

func writeGroupsPostInst(w *bytes.Buffer, groups []dalec.AddGroupConfig) {
	for _, g := range groups {
		fmt.Fprintf(w, "getent group %s >/dev/null || groupadd --system %s\n", g.Name, g.Name)
	}
}

func setArtifactOwnershipPostInst(w *bytes.Buffer, spec *dalec.Spec, target string) {
	artifacts := spec.GetArtifacts(target)

	apply := func(artifacts map[string]dalec.ArtifactConfig, root string) {
		if artifacts == nil {
			return
		}
		sorted := dalec.SortMapKeys(artifacts)
		for _, key := range sorted {
			cfg := artifacts[key]
			resolved := cfg.ResolveName(key)
			p := filepath.Join(root, cfg.SubPath, resolved)
			if cfg.User != "" {
				fmt.Fprintf(w, "chown -R %s \"$DESTDIR%s\"\n", cfg.User, p)
			}
			if cfg.Group != "" {
				fmt.Fprintf(w, "chgrp -R %s \"$DESTDIR%s\"\n", cfg.Group, p)
			}
		}
	}

	apply(artifacts.Binaries, BinariesPath)
	apply(artifacts.ConfigFiles, ConfigFilesPath)
	apply(artifacts.Manpages, filepath.Join(ManpagesPath, spec.Name))
	apply(artifacts.Headers, HeadersPath)
	apply(artifacts.Licenses, filepath.Join(LicensesPath, spec.Name))
	apply(artifacts.Docs, filepath.Join(DocsPath, spec.Name))
	apply(artifacts.Libs, LibsPath)
	apply(artifacts.Libexec, LibexecPath)
	apply(artifacts.DataDirs, DataDirsPath)

	applyDir := func(dirs map[string]dalec.ArtifactDirConfig, root string) {
		sorted := dalec.SortMapKeys(dirs)
		for _, key := range sorted {
			cfg := dirs[key]
			p := filepath.Join(root, key)
			if cfg.User != "" {
				fmt.Fprintf(w, "chown -R %s \"$DESTDIR%s\"\n", cfg.User, p)
			}
			if cfg.Group != "" {
				fmt.Fprintf(w, "chgrp -R %s \"$DESTDIR%s\"\n", cfg.Group, p)
			}
		}
	}
	if artifacts.Directories != nil {
		applyDir(artifacts.Directories.Config, ConfigFilesPath)
		applyDir(artifacts.Directories.State, "/var/lib")
	}

	if len(artifacts.Links) > 0 {
		fmt.Fprintf(w, "# Set ownership for artifact symlinks\n")
		for _, link := range artifacts.Links {
			if link.User != "" {
				fmt.Fprintf(w, "chown -h %s \"$DESTDIR%s\"\n", link.User, link.Dest)
			}
			if link.Group != "" {
				fmt.Fprintf(w, "chgrp -h %s \"$DESTDIR%s\"\n", link.Group, link.Dest)
			}
		}
	}
}

// generateReadmeDebian creates the content for debian/README.Debian that documents
// source provenance, allowing consumers to understand where sources came from.
func generateReadmeDebian(spec *dalec.Spec) []byte {
	if len(spec.Sources) == 0 {
		return nil
	}

	buf := bytes.NewBuffer(nil)
	
	fmt.Fprintf(buf, "%s for Debian\n", spec.Name)
	fmt.Fprintf(buf, "%s\n\n", strings.Repeat("=", len(spec.Name)+11)) // Create underline
	
	if spec.Description != "" {
		fmt.Fprintf(buf, "%s\n\n", spec.Description)
	}
	
	fmt.Fprintf(buf, "Source Provenance\n")
	fmt.Fprintf(buf, "-----------------\n\n")
	fmt.Fprintf(buf, "This package was built from the following sources:\n\n")
	
	// Sort source names for consistent output
	sorted := dalec.SortMapKeys(spec.Sources)
	for i, name := range sorted {
		if i > 0 {
			fmt.Fprintf(buf, "\n")
		}
		
		fmt.Fprintf(buf, "Source: %s\n", name)
		
		src := spec.Sources[name]
		doc := src.Doc(name)
		scanner := bufio.NewScanner(doc)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line != "" {
				fmt.Fprintf(buf, "  %s\n", line)
			}
		}
		if err := scanner.Err(); err != nil {
			fmt.Fprintf(buf, "  Error reading source documentation: %v\n", err)
		}
	}
	
	fmt.Fprintf(buf, "\n")
	return buf.Bytes()
}
