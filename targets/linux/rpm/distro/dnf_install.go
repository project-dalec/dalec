package distro

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/packaging/linux/rpm"
)

var dnfRepoPlatform = dalec.RepoPlatformConfig{
	ConfigRoot: "/etc/yum.repos.d",
	GPGKeyRoot: "/etc/pki/rpm-gpg",
	ConfigExt:  ".repo",
}

type PackageInstaller func(*dnfInstallConfig, string, []string) llb.RunOption

type dnfInstallConfig struct {
	// path for gpg keys to import for using a repo. These files for these keys
	// must also be added as mounts
	keys []string

	// Sets the root path to install rpms too.
	// this acts like installing to a chroot.
	root string

	// Additional mounts to add to the (t?)dnf install command (useful if installing RPMS which are mounted to a local directory)
	mounts []llb.RunOption

	constraints []llb.ConstraintsOpt

	downloadOnly bool

	allDeps bool

	downloadDir string

	// When true, don't omit docs from the installed RPMs.
	includeDocs bool
}

type DnfInstallOpt func(*dnfInstallConfig)

// see comment in tdnfInstall for why this additional option is needed
func DnfImportKeys(keys []string) DnfInstallOpt {
	return func(cfg *dnfInstallConfig) {
		cfg.keys = append(cfg.keys, keys...)
	}
}

func DnfWithMounts(opts ...llb.RunOption) DnfInstallOpt {
	return func(cfg *dnfInstallConfig) {
		cfg.mounts = append(cfg.mounts, opts...)
	}
}

func DnfAtRoot(root string) DnfInstallOpt {
	return func(cfg *dnfInstallConfig) {
		cfg.root = root
	}
}

func DnfDownloadAllDeps(dest string) DnfInstallOpt {
	return func(cfg *dnfInstallConfig) {
		cfg.downloadOnly = true
		cfg.allDeps = true
		cfg.downloadDir = dest
	}
}

func IncludeDocs(v bool) DnfInstallOpt {
	return func(cfg *dnfInstallConfig) {
		cfg.includeDocs = v
	}
}

func DnfInstallWithConstraints(opts []llb.ConstraintsOpt) DnfInstallOpt {
	return func(cfg *dnfInstallConfig) {
		cfg.constraints = append(cfg.constraints, opts...)
	}
}

func dnfInstallFlags(cfg *dnfInstallConfig) string {
	var cmdOpts string

	if cfg.root != "" {
		cmdOpts += " --installroot=" + cfg.root
		cmdOpts += " --setopt=reposdir=/etc/yum.repos.d"
	}

	if cfg.downloadOnly {
		cmdOpts += " --downloadonly"
	}

	if cfg.allDeps {
		cmdOpts += " --alldeps"
	}

	if cfg.downloadDir != "" {
		cmdOpts += " --downloaddir " + cfg.downloadDir
	}

	if !cfg.includeDocs {
		cmdOpts += " --setopt=tsflags=nodocs"
	}

	return cmdOpts
}

func dnfInstallOptions(cfg *dnfInstallConfig, opts []DnfInstallOpt) {
	for _, o := range opts {
		o(cfg)
	}
}

func importGPGScript(keyPaths []string) string {
	// all keys that are included should be mounted under this path
	keyRoot := "/etc/pki/rpm-gpg"

	importScript := "#!/usr/bin/env sh\nset -eux\n"
	for _, keyPath := range keyPaths {
		keyName := filepath.Base(keyPath)
		fullPath := filepath.Join(keyRoot, keyName)
		// rpm --import requires armored keys, check if key is armored and convert if needed
		importScript += fmt.Sprintf(`
key_path="%s"
gpg --import --armor "${key_path}"

if ! head -1 "${key_path}" | grep -q 'BEGIN PGP PUBLIC KEY BLOCK'; then
	gpg --armor --export > /tmp/key.asc
	key_path="/tmp/key.asc"
fi
rpm --import "${key_path}"
`, fullPath)
	}

	return importScript
}

func dnfCommand(cfg *dnfInstallConfig, releaseVer string, exe string, dnfSubCmd []string, dnfArgs []string) llb.RunOption {
	const importKeysPath = "/tmp/dalec/internal/dnf/import-keys.sh"

	cacheDir := "/var/cache/" + exe
	if cfg.root != "" {
		cacheDir = filepath.Join(cfg.root, cacheDir)
	}
	installFlags := dnfInstallFlags(cfg)
	installFlags += " -y --setopt varsdir=/etc/dnf/vars --releasever=" + releaseVer + " "
	installScriptDt := `#!/usr/bin/env bash
set -eux -o pipefail

import_keys_path="` + importKeysPath + `"
cmd="` + exe + `"
install_flags="` + installFlags + `"
dnf_sub_cmd="` + strings.Join(dnfSubCmd, " ") + `"
cache_dir="` + cacheDir + `"

if [ -x "$import_keys_path" ]; then
	"$import_keys_path"
fi

$cmd $dnf_sub_cmd $install_flags "${@}"
`
	var runOpts []llb.RunOption

	installScript := llb.Scratch().File(llb.Mkfile("install.sh", 0o700, []byte(installScriptDt)), cfg.constraints...)
	const installScriptPath = "/tmp/dalec/internal/dnf/install.sh"

	runOpts = append(runOpts, llb.AddMount(installScriptPath, installScript, llb.SourcePath("install.sh"), llb.Readonly))

	// TODO(adamperlin): see if this can be removed for dnf
	// If we have keys to import in order to access a repo, we need to create a script to use `gpg` to import them
	// This is an unfortunate consequence of a bug in tdnf (see https://github.com/vmware/tdnf/issues/471).
	// The keys *should* be imported automatically by tdnf as long as the repo config references them correctly and
	// we mount the key files themselves under the right path. However, tdnf does NOT do this
	// currently if the keys are referenced via a `file:///` type url,
	// and we must manually import the keys as well.
	if len(cfg.keys) > 0 {
		importScript := importGPGScript(cfg.keys)
		runOpts = append(runOpts, llb.AddMount(importKeysPath,
			llb.Scratch().File(llb.Mkfile("/import-keys.sh", 0755, []byte(importScript)), cfg.constraints...),
			llb.Readonly,
			llb.SourcePath("/import-keys.sh")))
	}

	cmd := make([]string, 0, len(dnfArgs)+1)
	cmd = append(cmd, installScriptPath)
	cmd = append(cmd, dnfArgs...)

	runOpts = append(runOpts, llb.Args(cmd))
	runOpts = append(runOpts, cfg.mounts...)

	return dalec.WithRunOptions(runOpts...)
}

func DnfInstall(cfg *dnfInstallConfig, releaseVer string, pkgs []string) llb.RunOption {
	return dnfCommand(cfg, releaseVer, "dnf", append([]string{"install"}, pkgs...), nil)
}

func TdnfInstall(cfg *dnfInstallConfig, releaseVer string, pkgs []string) llb.RunOption {
	return dnfCommand(cfg, releaseVer, "tdnf", append([]string{"install"}, pkgs...), nil)
}

func (cfg *Config) InstallBuildDeps(spec *dalec.Spec, sOpt dalec.SourceOpts, targetKey string, opts ...llb.ConstraintsOpt) llb.StateOption {
	deps := spec.GetPackageDeps(targetKey).GetBuild()
	if len(deps) == 0 {
		return dalec.NoopStateOption
	}

	repos := spec.GetBuildRepos(targetKey)
	return cfg.WithDeps(sOpt, targetKey, spec.Name, deps, repos, opts...)
}

func (cfg *Config) WithDeps(sOpt dalec.SourceOpts, targetKey, pkgName string, deps dalec.PackageDependencyList, repos []dalec.PackageRepositoryConfig, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		if len(deps) == 0 {
			return in
		}

		opts = append(opts, dalec.ProgressGroup(fmt.Sprintf("Installing dependencies for %s", pkgName)))

		spec := &dalec.Spec{
			Name:        fmt.Sprintf("%s-dependencies", pkgName),
			Description: "Virtual Package to install dependencies for " + pkgName,
			Version:     "1.0",
			License:     "Apache 2.0",
			Revision:    "1",
			Dependencies: &dalec.PackageDependencies{
				Runtime: deps,
			},
		}

		rpmSpec := rpm.RPMSpec(spec, in, targetKey, "", opts...)

		specPath := filepath.Join("SPECS", spec.Name, spec.Name+".spec")
		cacheInfo := rpm.CacheInfo{TargetKey: targetKey, Caches: spec.Build.Caches}
		rpmDir := rpm.Build(rpmSpec, in, specPath, cacheInfo, opts...)

		const rpmMountDir = "/tmp/internal/dalec/deps/install/rpms"

		repoMounts, keyPaths := cfg.RepoMounts(repos, sOpt, opts...)

		installOpts := []DnfInstallOpt{
			DnfWithMounts(llb.AddMount(rpmMountDir, rpmDir, llb.SourcePath("/RPMS"), llb.Readonly)),
			DnfWithMounts(repoMounts),
			DnfImportKeys(keyPaths),
			DnfInstallWithConstraints(opts),
		}

		install := cfg.Install([]string{filepath.Join(rpmMountDir, "*/*.rpm")}, installOpts...)
		return in.Run(
			dalec.WithConstraints(opts...),
			deps.GetSourceLocation(in),
			install,
		).Root()
	}
}

func (cfg *Config) DownloadDeps(sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, constraints dalec.PackageDependencyList, opts ...llb.ConstraintsOpt) llb.State {
	if constraints == nil {
		return llb.Scratch()
	}

	opts = append(opts, dalec.ProgressGroup("Downloading dependencies"))

	worker := cfg.Worker(sOpt, dalec.Platform(sOpt.TargetPlatform), dalec.WithConstraints(opts...))

	installOpts := []DnfInstallOpt{
		DnfInstallWithConstraints(opts),
	}

	worker = worker.Run(
		dalec.WithConstraints(opts...),
		cfg.Install([]string{"dnf-utils"}, installOpts...),
	).Root()

	args := []string{"--downloaddir", "/output", "download"}
	for name, constraint := range constraints {
		if len(constraint.Version) == 0 {
			args = append(args, name)
			continue
		}
		for _, version := range constraint.Version {
			args = append(args, fmt.Sprintf("%s %s", name, rpm.FormatVersionConstraint(version)))
		}
	}

	installTimeRepos := spec.GetInstallRepos(targetKey)
	repoMounts, keyPaths := cfg.RepoMounts(installTimeRepos, sOpt, opts...)

	installOpts = append(installOpts,
		DnfWithMounts(repoMounts),
		DnfImportKeys(keyPaths),
	)

	var installCfg dnfInstallConfig
	dnfInstallOptions(&installCfg, installOpts)

	return worker.Run(
		dalec.WithRunOptions(dnfCommand(&installCfg, cfg.ReleaseVer, "dnf", nil, args)),
		dalec.WithConstraints(opts...),
	).AddMount("/output", llb.Scratch())
}
