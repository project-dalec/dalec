package distro

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
)

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
	// Packages are passed as positional args ("${@}") rather than baked into the
	// subcommand so that install flags (--allow-downgrade, etc.) render before
	// the package operands. zypper rejects install flags that appear after a
	// package name ("'--allow-downgrade' is not a package name or capability").
	return zypperCommand(cfg, []string{"install"}, pkgs)
}

func zypperCommand(cfg *dnfInstallConfig, zypperSubCmd []string, zypperArgs []string) llb.RunOption {
	const importKeysPath = "/tmp/dalec/internal/zypper/import-keys.sh"

	// zypper cannot install into a foreign-architecture rootfs the way dnf can
	// (it has no --forcearch). cfg.root is only set for cross-arch/rootfs
	// installs, which are guarded out for zypper-based distros at the worker
	// level (Config.CrossArchInstallUnsupported), so this is a best-effort
	// native rootfs install only.
	globalFlags := []string{"--non-interactive", "--gpg-auto-import-keys"}
	if cfg.root != "" {
		globalFlags = append(globalFlags, "--root", cfg.root)
	}

	// Global zypper flags: run unattended and auto-import repo signing keys.
	globalFlagsStr := strings.Join(globalFlags, " ")
	// Install-time flags: accept licenses non-interactively and tolerate the
	// vendor/version differences that arise when pulling from the Microsoft
	// prod repos alongside the base SUSE repos.
	installFlags := []string{"--auto-agree-with-licenses", "--allow-downgrade", "--allow-vendor-change"}
	installFlagsStr := strings.Join(installFlags, " ")
	zypperSubCmdStr := strings.Join(zypperSubCmd, " ")

	// zypperInstallScriptTmpl renders the installer shell script.
	// Scalar values are shell-quoted with %q to preserve safe literal values.
	zypperInstallScriptTmpl := template.Must(template.New("zypper-install").Funcs(template.FuncMap{
		"shellQuote": func(s string) string { return fmt.Sprintf("%q", s) },
	}).Parse(`#!/usr/bin/env bash
set -eux -o pipefail

import_keys_path={{ shellQuote .ImportKeysPath }}
global_flags={{ shellQuote .GlobalFlags }}
zypper_sub_cmd={{ shellQuote .ZypperSubCmd }}
install_flags={{ shellQuote .InstallFlags }}
root_dir={{ shellQuote .Root }}

if [ -x "$import_keys_path" ]; then
	"$import_keys_path"
fi

{{ if not .IncludeDocs }}
# zypper/libzypp has no command-line flag equivalent to dnf's
# --setopt=tsflags=nodocs: passing "--rpm-installexcludedocs" is rejected as an
# unknown option and fails the whole install before any package is laid down.
# libzypp (not the zypper CLI) controls documentation exclusion via the
# rpm.install.excludedocs option in zypp.conf. Enable it in the config file
# libzypp will read for this install (honoring --root when set).
zypp_conf="${root_dir}/etc/zypp/zypp.conf"
mkdir -p "$(dirname "$zypp_conf")"
if [ -f "$zypp_conf" ] && grep -Eq '^[[:space:]]*#?[[:space:]]*rpm\.install\.excludedocs' "$zypp_conf"; then
	sed -i -E 's|^[[:space:]]*#?[[:space:]]*rpm\.install\.excludedocs.*|rpm.install.excludedocs = yes|' "$zypp_conf"
else
	printf '\nrpm.install.excludedocs = yes\n' >> "$zypp_conf"
fi
{{ end }}

zypper $global_flags $zypper_sub_cmd $install_flags "${@}"
`))

	var installScriptBuf bytes.Buffer
	err := zypperInstallScriptTmpl.Execute(&installScriptBuf, struct {
		ImportKeysPath string
		GlobalFlags    string
		ZypperSubCmd   string
		InstallFlags   string
		Root           string
		IncludeDocs    bool
	}{
		ImportKeysPath: importKeysPath,
		GlobalFlags:    globalFlagsStr,
		ZypperSubCmd:   zypperSubCmdStr,
		InstallFlags:   installFlagsStr,
		Root:           cfg.root,
		IncludeDocs:    cfg.includeDocs,
	})
	if err != nil {
		panic(fmt.Errorf("rendering zypper install script: %w", err))
	}

	installScript := llb.Scratch().File(llb.Mkfile("install.sh", 0o700, installScriptBuf.Bytes()), cfg.constraints...)
	const installScriptPath = "/tmp/dalec/internal/zypper/install.sh"

	runOpts := []llb.RunOption{
		llb.AddMount(installScriptPath, installScript, llb.SourcePath("install.sh"), llb.Readonly),
	}

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
