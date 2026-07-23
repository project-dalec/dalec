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
	//
	// For the default (worker / no-rootfs) install path we deliberately do NOT
	// pass --no-gpg-checks: that would disable signature verification for
	// *repository* packages too, which would defeat negative tests (e.g.
	// installing a build dependency from a signed repo whose public key was not
	// provided must fail). Instead repo packages are verified normally
	// (--gpg-auto-import-keys imports keys advertised by the repo config), while
	// the locally-built, unsigned dalec rpms passed as command-line file operands
	// are permitted via the install-time --allow-unsigned-rpm flag below.
	globalFlags := []string{"--non-interactive", "--gpg-auto-import-keys"}
	if cfg.root != "" {
		// --installroot (not --root): install into cfg.root while still reading
		// repositories and configuration from the host. --root would make zypper
		// look for repos under the empty target root and fail with "no enabled
		// repositories". This mirrors dnf's --installroot behavior.
		globalFlags = append(globalFlags, "--installroot", cfg.root)

		// The --installroot path is used when assembling a container from a
		// custom base image: dalec's own freshly built rpm files are installed as
		// command-line operands into a fresh rootfs. Those rpms may be signed
		// (rpmsign --addsign) with a key that is not present in the target
		// rootfs's rpm keyring, so zypper would reject them. --allow-unsigned-rpm
		// only covers *unsigned* files, not signed-but-untrusted ones, so add
		// --no-gpg-checks for this path only. This mirrors dnf/tdnf, whose default
		// localpkg_gpgcheck=0 skips GPG verification for command-line package
		// files while still verifying repository packages. The repository-verified
		// negative tests use the worker install path (cfg.root == "") and are
		// therefore unaffected.
		globalFlags = append(globalFlags, "--no-gpg-checks")
	}

	// Global zypper flags: run unattended and auto-import repo signing keys.
	globalFlagsStr := strings.Join(globalFlags, " ")
	// Install-time flags: accept licenses non-interactively, tolerate the
	// vendor/version differences that arise when pulling from the Microsoft
	// prod repos alongside the base SUSE repos, and silently install the
	// unsigned, locally-built dalec rpm files given as command-line operands
	// (--allow-unsigned-rpm applies only to command-line rpm files, not to
	// packages resolved from repositories, which remain signature-verified).
	installFlags := []string{"--auto-agree-with-licenses", "--allow-downgrade", "--allow-vendor-change", "--allow-unsigned-rpm"}
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

if [ -x "$import_keys_path" ]; then
	"$import_keys_path"
fi

{{ if not .IncludeDocs }}
# zypper/libzypp has no command-line flag equivalent to dnf's
# --setopt=tsflags=nodocs: passing "--rpm-installexcludedocs" is rejected as an
# unknown option and fails the whole install before any package is laid down.
# libzypp (not the zypper CLI) controls documentation exclusion via the
# rpm.install.excludedocs option in zypp.conf. With --installroot, libzypp still
# reads its configuration from the host, so enable the option in the host
# /etc/zypp/zypp.conf.
zypp_conf="/etc/zypp/zypp.conf"
mkdir -p "$(dirname "$zypp_conf")"
if [ -f "$zypp_conf" ] && grep -Eq '^[[:space:]]*#?[[:space:]]*rpm\.install\.excludedocs' "$zypp_conf"; then
	sed -i -E 's|^[[:space:]]*#?[[:space:]]*rpm\.install\.excludedocs.*|rpm.install.excludedocs = yes|' "$zypp_conf"
else
	printf '\nrpm.install.excludedocs = yes\n' >> "$zypp_conf"
fi
{{ else }}
# When docs are requested, explicitly force rpm.install.excludedocs = no.
# bci-base ships /etc/zypp/zypp.conf with "rpm.install.excludedocs = yes" enabled
# by default, so unlike dnf (which keeps docs unless told otherwise) libzypp would
# strip documentation even though the caller asked to include it. Override the
# base-image default so documentation artifacts are actually installed.
zypp_conf="/etc/zypp/zypp.conf"
mkdir -p "$(dirname "$zypp_conf")"
if [ -f "$zypp_conf" ] && grep -Eq '^[[:space:]]*#?[[:space:]]*rpm\.install\.excludedocs' "$zypp_conf"; then
	sed -i -E 's|^[[:space:]]*#?[[:space:]]*rpm\.install\.excludedocs.*|rpm.install.excludedocs = no|' "$zypp_conf"
else
	printf '\nrpm.install.excludedocs = no\n' >> "$zypp_conf"
fi
{{ end }}

# zypper does not expand shell globs in local-file operands the way dnf does
# (dnf expands "*/*.rpm" internally). The container install path passes glob
# patterns like /tmp/rpms/**/*.rpm, so expand them here before invoking zypper.
# Plain package names (no glob metacharacters) pass through unchanged; a glob
# that matches nothing is dropped (nullglob) rather than sent literally, which
# zypper would reject as a nonexistent local path.
shopt -s globstar nullglob
install_args=()
for arg in "${@}"; do
	case "$arg" in
	*[*?[]*)
		expanded=( $arg )
		install_args+=( "${expanded[@]}" )
		;;
	*)
		install_args+=( "$arg" )
		;;
	esac
done

zypper $global_flags $zypper_sub_cmd $install_flags "${install_args[@]}"
`))

	var installScriptBuf bytes.Buffer
	err := zypperInstallScriptTmpl.Execute(&installScriptBuf, struct {
		ImportKeysPath string
		GlobalFlags    string
		ZypperSubCmd   string
		InstallFlags   string
		IncludeDocs    bool
	}{
		ImportKeysPath: importKeysPath,
		GlobalFlags:    globalFlagsStr,
		ZypperSubCmd:   zypperSubCmdStr,
		InstallFlags:   installFlagsStr,
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
