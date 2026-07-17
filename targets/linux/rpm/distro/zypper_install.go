package distro

import (
	"strings"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
)

// zypperRepoPlatform describes where zypper (SUSE/openSUSE) expects repository
// configuration and signing keys to live. It mirrors dnfRepoPlatform but points
// at zypper's repo directory (/etc/zypp/repos.d) instead of /etc/yum.repos.d.
var zypperRepoPlatform = dalec.RepoPlatformConfig{
	ConfigRoot: "/etc/zypp/repos.d",
	GPGKeyRoot: "/etc/pki/rpm-gpg",
	ConfigExt:  ".repo",
}

// ZypperInstall installs packages using zypper, the package manager used by
// SUSE Linux Enterprise and openSUSE. It satisfies the PackageInstaller
// signature so it can be plugged into a distro Config's InstallFunc, and it
// reuses the shared dnfInstallConfig option plumbing (keys, root, mounts,
// constraints) so callers use the same DnfInstallOpt set as the dnf/tdnf paths.
//
// releaseVer is accepted for signature compatibility but is unused: unlike dnf,
// zypper does not take a --releasever flag (the release is fixed by the base
// image).
func ZypperInstall(cfg *dnfInstallConfig, releaseVer string, pkgs []string) llb.RunOption {
	return zypperCommand(cfg, append([]string{"install"}, pkgs...), nil)
}

func zypperCommand(cfg *dnfInstallConfig, zypperSubCmd []string, zypperArgs []string) llb.RunOption {
	const importKeysPath = "/tmp/dalec/internal/zypper/import-keys.sh"

	// zypper cannot install into a foreign-architecture rootfs the way dnf can
	// (it has no --forcearch). cfg.root is only set for cross-arch/rootfs
	// installs, which are guarded out for zypper-based distros at the worker
	// level (Config.CrossArchInstallUnsupported), so this is a best-effort
	// native rootfs install only.
	rootFlag := ""
	if cfg.root != "" {
		rootFlag = "--root " + cfg.root + " "
	}

	// Global zypper flags: run unattended and auto-import repo signing keys.
	globalFlags := "--non-interactive --gpg-auto-import-keys " + rootFlag
	// Install-time flags: accept licenses non-interactively and tolerate the
	// vendor/version differences that arise when pulling from the Microsoft
	// prod repos alongside the base SUSE repos.
	installFlags := "--auto-agree-with-licenses --allow-downgrade --allow-vendor-change"

	installScriptDt := `#!/usr/bin/env bash
set -eux -o pipefail

import_keys_path="` + importKeysPath + `"
global_flags="` + globalFlags + `"
zypper_sub_cmd="` + strings.Join(zypperSubCmd, " ") + `"
install_flags="` + installFlags + `"

if [ -x "$import_keys_path" ]; then
	"$import_keys_path"
fi

zypper $global_flags $zypper_sub_cmd $install_flags "${@}"
`
	var runOpts []llb.RunOption

	installScript := llb.Scratch().File(llb.Mkfile("install.sh", 0o700, []byte(installScriptDt)), cfg.constraints...)
	const installScriptPath = "/tmp/dalec/internal/zypper/install.sh"

	runOpts = append(runOpts, llb.AddMount(installScriptPath, installScript, llb.SourcePath("install.sh"), llb.Readonly))

	// If we have keys to import in order to access a repo, mount a script that
	// imports them into the rpm keyring (zypper uses the same rpm keyring).
	// zypper's --gpg-auto-import-keys covers most cases, but importing up front
	// keeps parity with the dnf path for repos that reference keys via file://.
	if len(cfg.keys) > 0 {
		importScript := importGPGScript(cfg.keys)
		runOpts = append(runOpts, llb.AddMount(importKeysPath,
			llb.Scratch().File(llb.Mkfile("/import-keys.sh", 0755, []byte(importScript)), cfg.constraints...),
			llb.Readonly,
			llb.SourcePath("/import-keys.sh")))
	}

	cmd := make([]string, 0, len(zypperArgs)+1)
	cmd = append(cmd, installScriptPath)
	cmd = append(cmd, zypperArgs...)

	runOpts = append(runOpts, llb.Args(cmd))
	runOpts = append(runOpts, cfg.mounts...)

	return dalec.WithRunOptions(runOpts...)
}
