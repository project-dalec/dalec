package distro

import (
	"bytes"
	"fmt"
	"path/filepath"
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

// ZypperUpgrade upgrades installed packages using zypper. With no packages,
// every installed package with an available update is upgraded. Packages
// excluded with [DnfExclude] are never upgraded: zypper has no exclude option,
// so they are left out of the upgrade and the build fails if the upgrade
// changed them anyway (for example to satisfy a dependency).
func ZypperUpgrade(cfg *dnfInstallConfig, releaseVer string, pkgs []string) llb.RunOption {
	return zypperCommand(cfg, []string{"update"}, upgradeArgs(cfg, pkgs))
}

const zypperProxyConfigScript = `
cleanup_zypper_proxy() {
	if [ -n "${DALEC_ZYPPER_PROXY_ANCHOR_ACTIVE:-}" ]; then
		rm -f "${DALEC_ZYPPER_PROXY_ANCHOR_ACTIVE}"
		"${DALEC_ZYPPER_UPDATE_CA_CERTIFICATES:-update-ca-certificates}"
		unset DALEC_ZYPPER_PROXY_ANCHOR_ACTIVE
	fi
}

install_zypper_proxy_ca() {
	source_bundle="${1}"
	if [ ! -f "${source_bundle}" ]; then
		return 0
	fi
	if ! grep -q '# buildkit proxy CA begin' "${source_bundle}" 2>/dev/null; then
		return 0
	fi

	proxy_anchor="${DALEC_ZYPPER_PROXY_ANCHOR:-/etc/pki/trust/anchors/dalec-buildkit-proxy-ca.pem}"
	mkdir -p "$(dirname "${proxy_anchor}")"
	sed -n '/# buildkit proxy CA begin/,/# buildkit proxy CA end/p' "${source_bundle}" > "${proxy_anchor}"
	"${DALEC_ZYPPER_UPDATE_CA_CERTIFICATES:-update-ca-certificates}"
	DALEC_ZYPPER_PROXY_ANCHOR_ACTIVE="${proxy_anchor}"
}

configure_zypper_proxy() {
	restore_xtrace=0
	case "$-" in
		*x*) set +x; restore_xtrace=1 ;;
	esac

	if [ "${DALEC_DISABLE_PROXY_CONFIG:-}" = "1" ]; then
		if [ "${restore_xtrace}" = "1" ]; then
			set -x
		fi
		return 0
	fi

	http_proxy_value="${HTTP_PROXY:-${http_proxy:-}}"
	https_proxy_value="${HTTPS_PROXY:-${https_proxy:-}}"
	if [ -z "${http_proxy_value}" ] && [ -z "${https_proxy_value}" ]; then
		if [ "${restore_xtrace}" = "1" ]; then
			set -x
		fi
		return 0
	fi

	zypper_proxy_ca_bundle="${DALEC_RPM_PROXY_CA_BUNDLE:-}"
	if [ -n "${zypper_proxy_ca_bundle}" ] && [ ! -f "${zypper_proxy_ca_bundle}" ]; then
		zypper_proxy_ca_bundle=""
	fi
	if [ -z "${zypper_proxy_ca_bundle}" ]; then
		for ca_bundle in \
			/etc/ssl/certs/ca-certificates.crt \
			/etc/pki/tls/certs/ca-bundle.crt \
			/etc/ssl/ca-bundle.pem \
			/etc/pki/tls/cacert.pem \
			/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem \
			/etc/ssl/cert.pem
		do
			if [ -f "${ca_bundle}" ]; then
				zypper_proxy_ca_bundle="${ca_bundle}"
				break
			fi
		done
	fi

	if [ -n "${zypper_proxy_ca_bundle}" ]; then
		install_zypper_proxy_ca "${zypper_proxy_ca_bundle}"
	fi

	if [ "${restore_xtrace}" = "1" ]; then
		set -x
	fi
}
`

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
	}
	// Global zypper flags: run unattended and auto-import repo signing keys.
	globalFlagsStr := strings.Join(globalFlags, " ")
	// Install-time flags: accept licenses non-interactively and tolerate the
	// vendor/version differences that arise when pulling from the Microsoft
	// prod repos alongside the base SUSE repos.
	installFlags := []string{"--auto-agree-with-licenses", "--allow-downgrade", "--allow-vendor-change"}
	update := len(zypperSubCmd) == 1 && zypperSubCmd[0] == "update"
	if update {
		// Only upgrade what is installed: recommended packages of the upgraded
		// packages are not pulled in.
		installFlags = []string{"--auto-agree-with-licenses", "--allow-vendor-change", "--no-recommends"}
	}
	installFlagsStr := strings.Join(installFlags, " ")
	zypperSubCmdStr := strings.Join(zypperSubCmd, " ")

	// zypperInstallScriptTmpl renders the installer shell script.
	// Scalar values are shell-quoted with %q to preserve safe literal values.
	zypperInstallScriptTmpl := template.Must(template.New("zypper-install").Funcs(template.FuncMap{
		"shellQuote": func(s string) string { return fmt.Sprintf("%q", s) },
	}).Parse(`#!/usr/bin/env bash
set -eux -o pipefail

` + zypperProxyConfigScript + `

import_keys_path={{ shellQuote .ImportKeysPath }}
global_flags={{ shellQuote .GlobalFlags }}
zypper_sub_cmd={{ shellQuote .ZypperSubCmd }}
install_flags={{ shellQuote .InstallFlags }}
post_install_path={{ shellQuote .PostInstallPath }}
install_root={{ shellQuote .Root }}

if [ -x "$import_keys_path" ]; then
	"$import_keys_path"
fi

configure_zypper_proxy
trap cleanup_zypper_proxy EXIT

# zypper/libzypp has no command-line flag equivalent to dnf's
# --setopt=tsflags=nodocs (passing "--rpm-installexcludedocs" is rejected as an
# unknown option and fails the whole install before any package is laid down).
# libzypp (not the zypper CLI) controls documentation exclusion via the
# rpm.install.excludedocs option in zypp.conf. With --installroot, libzypp still
# reads its configuration from the host, so set the option in the host
# /etc/zypp/zypp.conf. bci-base ships zypp.conf with
# "rpm.install.excludedocs = yes" enabled, so when docs ARE requested we must
# explicitly force it to "no" to override that base-image default.
excludedocs={{ if .IncludeDocs }}no{{ else }}yes{{ end }}
zypp_conf="/etc/zypp/zypp.conf"
mkdir -p "$(dirname "$zypp_conf")"
if [ -f "$zypp_conf" ] && grep -Eq '^[[:space:]]*#?[[:space:]]*rpm\.install\.excludedocs' "$zypp_conf"; then
	sed -i -E "s|^[[:space:]]*#?[[:space:]]*rpm\.install\.excludedocs.*|rpm.install.excludedocs = $excludedocs|" "$zypp_conf"
else
	printf '\nrpm.install.excludedocs = %s\n' "$excludedocs" >> "$zypp_conf"
fi

# Put locally-built Dalec RPMs in an ephemeral plaindir repository rather than
# installing them as command-line files. This lets us disable package GPG checks
# only for the trusted mounted artifacts; external repositories keep their
# configured verification policy, and no build-time signing key is imported
# into the target root's RPM database.
shopt -s globstar nullglob
install_args=()
local_rpms=()
excludes=()
for arg in "${@}"; do
	case "$arg" in
	--exclude=*)
		excludes+=( "${arg#--exclude=}" )
		;;
	*[*?[]*)
		expanded=( $arg )
		for expanded_arg in "${expanded[@]}"; do
			if [[ "$expanded_arg" == *.rpm && -f "$expanded_arg" ]]; then
				local_rpms+=( "$expanded_arg" )
			else
				install_args+=( "$expanded_arg" )
			fi
		done
		;;
	*)
		if [[ "$arg" == *.rpm && -f "$arg" ]]; then
			local_rpms+=( "$arg" )
		else
			install_args+=( "$arg" )
		fi
		;;
	esac
done

if (( ${#local_rpms[@]} > 0 )); then
	local_repo_alias="dalec-tmp-internal-local"
	local_repo_dir="/tmp/dalec/internal/zypper/local-repo"
	repos_dir="/tmp/dalec/internal/zypper/repos.d"
	mkdir -p "$local_repo_dir" "$repos_dir"

	if [ -d /etc/zypp/repos.d ]; then
		cp -a /etc/zypp/repos.d/. "$repos_dir/"
	fi

	for rpm_path in "${local_rpms[@]}"; do
		ln -sf "$rpm_path" "$local_repo_dir/$(basename "$rpm_path")"

		read -r name epoch version release arch < <(
			rpm -qp --queryformat '%{NAME} %{EPOCHNUM} %{VERSION} %{RELEASE} %{ARCH}\n' "$rpm_path"
		)
		if [[ "$epoch" != "0" && "$epoch" != "(none)" ]]; then
			version="${epoch}:${version}"
		fi
		install_args+=( "${local_repo_alias}:${name}-${version}-${release}.${arch}" )
	done

	cat > "$repos_dir/${local_repo_alias}.repo" <<EOF
[${local_repo_alias}]
name=Dalec mounted RPMs
enabled=1
autorefresh=1
baseurl=dir:${local_repo_dir}
type=plaindir
gpgcheck=0
priority=1
EOF

	global_flags="$global_flags --reposd-dir $repos_dir"
	zypper $global_flags refresh --force "$local_repo_alias"
fi

{{- if .Update }}
rpm_root=()
if [ -n "$install_root" ]; then
	rpm_root=( --root "$install_root" )
fi

# Versions of the excluded packages, checked after the upgrade.
excluded_versions() {
	if (( ${#excludes[@]} > 0 )); then
		rpm "${rpm_root[@]}" -q --qf '%{NAME}-%{EPOCHNUM}:%{VERSION}-%{RELEASE}.%{ARCH}\n' -- "${excludes[@]}" 2>/dev/null | grep -v 'is not installed' || true
	fi
}
before="$(excluded_versions)"

is_excluded() {
	local e
	for e in "${excludes[@]}"; do
		[ "$e" = "$1" ] && return 0
	done
	return 1
}

if (( ${#install_args[@]} == 0 )); then
	# zypper has no exclude option: list the available updates and leave out
	# the excluded packages.
	while read -r name; do
		if [ -n "$name" ] && ! is_excluded "$name"; then
			install_args+=( "$name" )
		fi
	done < <(zypper $global_flags --xmlout list-updates | grep -o '<update [^>]*>' | sed -n 's/.* name="\([^"]*\)".*/\1/p' | sort -u)
fi

if (( ${#install_args[@]} > 0 )); then
	zypper $global_flags $zypper_sub_cmd $install_flags "${install_args[@]}"
else
	echo "No packages to update"
fi

after="$(excluded_versions)"
if [ "$before" != "$after" ]; then
	echo "the update changed excluded packages:" >&2
	diff <(echo "$before") <(echo "$after") >&2 || true
	exit 1
fi
{{- else }}
zypper $global_flags $zypper_sub_cmd $install_flags "${install_args[@]}"
{{- end }}

if [ -n "$post_install_path" ]; then
	"$post_install_path"
fi
`))

	var installScriptBuf bytes.Buffer
	err := zypperInstallScriptTmpl.Execute(&installScriptBuf, struct {
		ImportKeysPath  string
		GlobalFlags     string
		ZypperSubCmd    string
		InstallFlags    string
		PostInstallPath string
		IncludeDocs     bool
		Update          bool
		Root            string
	}{
		ImportKeysPath:  importKeysPath,
		GlobalFlags:     globalFlagsStr,
		ZypperSubCmd:    zypperSubCmdStr,
		InstallFlags:    installFlagsStr,
		PostInstallPath: cfg.postInstallPath,
		IncludeDocs:     cfg.includeDocs,
		Update:          update,
		Root:            cfg.root,
	})
	if err != nil {
		// The template is a compile-time constant, so Execute realistically only
		// fails on a programmer error. Surface it as an errored llb state (which
		// fails the solve with this error) rather than panicking.
		// llb.StateOption implements llb.RunOption (its SetRunOption applies the
		// option to the exec's state), so returning ErrorStateOption directly is
		// sufficient.
		return dalec.ErrorStateOption(fmt.Errorf("rendering zypper install script: %w", err))
	}

	installScript := llb.Scratch().File(llb.Mkfile("install.sh", 0o700, installScriptBuf.Bytes()), cfg.constraints...)
	const installScriptPath = "/tmp/dalec/internal/zypper/install.sh"

	runOpts := []llb.RunOption{
		llb.AddMount(installScriptPath, installScript, llb.SourcePath("install.sh"), llb.Readonly),
	}
	if cfg.postInstallPath != "" {
		runOpts = append(runOpts, llb.AddMount(
			cfg.postInstallPath,
			cfg.postInstallScript,
			llb.SourcePath(filepath.Base(cfg.postInstallPath)),
			llb.Readonly,
		))
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
	if cfg.disableProxyConfig {
		runOpts = append(runOpts, llb.AddEnv(dalec.BuildArgDalecDisableProxyConfig, "1"))
	}

	return dalec.WithRunOptions(runOpts...)
}
