package distro

import (
	"context"
	"path/filepath"
	"strconv"

	"github.com/moby/buildkit/client/llb"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/packaging/linux/deb"
)

const aptProxyConfigScript = `
cleanup_apt_proxy() {
	if [ -n "${DALEC_APT_PROXY_CONFIG_ACTIVE:-}" ]; then
		rm -f "${DALEC_APT_PROXY_CONFIG_ACTIVE}" 2>/dev/null || true
		if [ "${APT_CONFIG:-}" = "${DALEC_APT_PROXY_CONFIG_ACTIVE}" ]; then
			if [ "${DALEC_APT_PROXY_CONFIG_OLD_APT_CONFIG_WAS_SET:-0}" = "1" ]; then
				export APT_CONFIG="${DALEC_APT_PROXY_CONFIG_OLD_APT_CONFIG}"
			else
				unset APT_CONFIG
			fi
		fi
		unset DALEC_APT_PROXY_CONFIG_ACTIVE
		unset DALEC_APT_PROXY_CONFIG_OLD_APT_CONFIG
		unset DALEC_APT_PROXY_CONFIG_OLD_APT_CONFIG_WAS_SET
	fi
}

configure_apt_proxy() {
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

	apt_proxy_conf="${DALEC_APT_PROXY_CONFIG:-/tmp/dalec/apt-proxy.conf}"
	apt_proxy_conf_dir="${apt_proxy_conf%/*}"
	if [ -n "${apt_proxy_conf_dir}" ] && [ "${apt_proxy_conf_dir}" != "${apt_proxy_conf}" ]; then
		mkdir -p "${apt_proxy_conf_dir}"
	fi

	write_apt_conf_string() {
		key="${1}"
		value="${2}"
		if [ -z "${value}" ]; then
			return 0
		fi
		escaped_value="$(printf '%s' "${value}" | sed 's/[\\"]/\\&/g')"
		printf '%s "%s";\n' "${key}" "${escaped_value}"
	}

	if [ -n "${APT_CONFIG+x}" ]; then
		DALEC_APT_PROXY_CONFIG_OLD_APT_CONFIG="${APT_CONFIG}"
		DALEC_APT_PROXY_CONFIG_OLD_APT_CONFIG_WAS_SET=1
	else
		unset DALEC_APT_PROXY_CONFIG_OLD_APT_CONFIG
		DALEC_APT_PROXY_CONFIG_OLD_APT_CONFIG_WAS_SET=0
	fi

	rm -f "${apt_proxy_conf}"
	(
		umask 077
		{
		write_apt_conf_string 'Acquire::http::Proxy' "${http_proxy_value}"
		write_apt_conf_string 'Acquire::https::Proxy' "${https_proxy_value}"
		for ca_bundle in \
			/etc/ssl/certs/ca-certificates.crt \
			/etc/pki/tls/certs/ca-bundle.crt \
			/etc/ssl/ca-bundle.pem \
			/etc/pki/tls/cacert.pem \
			/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem \
			/etc/ssl/cert.pem
		do
			if [ -f "${ca_bundle}" ]; then
				write_apt_conf_string 'Acquire::https::CaInfo' "${ca_bundle}"
				break
			fi
		done
		} > "${apt_proxy_conf}"
	)

	export APT_CONFIG="${apt_proxy_conf}"
	DALEC_APT_PROXY_CONFIG_ACTIVE="${apt_proxy_conf}"

	if [ "${restore_xtrace}" = "1" ]; then
		set -x
	fi
}
`

func aptProxyConfig(sOpt dalec.SourceOpts) llb.RunOption {
	return dalec.RunOptFunc(func(ei *llb.ExecInfo) {
		if sOpt.DisableProxyConfig() {
			llb.AddEnv(dalec.BuildArgDalecDisableProxyConfig, "1").SetRunOption(ei)
		}
	})
}

// AptInstall returns an [llb.RunOption] that uses apt to install the provided
// packages.
//
// This returns an [llb.RunOption] but it does create some things internally,
// This is what the constraints opts are used for.
// The constraints are applied after any constraint set on the [llb.ExecInfo]
func AptInstall(packages []string, opts ...llb.ConstraintsOpt) llb.RunOption {
	return dalec.RunOptFunc(func(ei *llb.ExecInfo) {
		const installScript = `#!/usr/bin/env sh
	set -ex

	` + aptProxyConfigScript + `

	configure_apt_proxy
	trap cleanup_apt_proxy EXIT

	# Make sure any cached data from local repos is purged since this should not
	# be shared between builds.
rm -f /var/lib/apt/lists/_*
apt autoclean -y 

# Remove any previously failed attempts to get repo data
rm -rf /var/lib/apt/lists/partial/*

apt update
apt install -y "$@"
`
		script := llb.Scratch().File(
			llb.Mkfile("install.sh", 0o755, []byte(installScript)),
			dalec.WithConstraint(&ei.Constraints),
			dalec.WithConstraints(opts...),
		)

		p := "/tmp/dalec/internal/deb/install.sh"
		llb.AddMount(p, script, llb.SourcePath("install.sh")).SetRunOption(ei)
		llb.AddEnv("DEBIAN_FRONTEND", "noninteractive").SetRunOption(ei)
		llb.Args(append([]string{p}, packages...)).SetRunOption(ei)
	})
}

// AptInstallIntoRoot installs packages into a mounted root filesystem (rootfsPath)
// while running apt/dpkg from the *current* container environment (i.e. build platform).
// Notes:
//   - This relies on the target rootfs containing valid /etc/apt and /var/lib/dpkg state.
//   - Maintainer scripts execute inside the target rootfs. If they invoke target-arch binaries,
//     they will require emulation (binfmt/qemu) on the build host.
//     This still avoids running the entire apt install pipeline under emulation: dependency
//     resolution/download/unpack are native; only maintainer scripts may be emulated.
func AptInstallIntoRoot(rootfsPath string, packages []string, targetArch string, buildPlat ocispecs.Platform) llb.RunOption {
	return dalec.RunOptFunc(func(ei *llb.ExecInfo) {
		// CRITICAL: this exec must run on the build platform (native),
		// not the target platform. Otherwise we hit exec format error when QEMU is off.
		bp := buildPlat
		ei.Constraints.Platform = &bp

		const installScript = `#!/bin/sh
	set -eu

	` + aptProxyConfigScript + `

	# Exit codes:
#   2 - Required environment variables missing
#   3 - Target rootfs invalid / missing apt sources
#   4 - Target rootfs dpkg arch mismatch
#   6 - No downloaded .deb files found for target arch

log() { echo "dalec(deb): $*" >&2; }

ROOTFS="${DALEC_ROOTFS:-}"
ARCH="${DALEC_TARGET_ARCH:-}"
	if [ -z "${ROOTFS}" ] || [ -z "${ARCH}" ]; then
	  log "DALEC_ROOTFS and DALEC_TARGET_ARCH must be set"
	  exit 2
	fi
	configure_apt_proxy
	trap cleanup_apt_proxy EXIT

	if [ -f "${ROOTFS}/var/lib/dpkg/arch" ]; then
  native_arch="$(head -n1 "${ROOTFS}/var/lib/dpkg/arch" 2>/dev/null | tr -d '\n' || true)"
  if [ -n "${native_arch}" ] && [ "${native_arch}" != "${ARCH}" ]; then
    log "target rootfs dpkg arch (${native_arch}) != requested (${ARCH})"
    exit 4
  fi
fi

SOURCELIST=""
SOURCEPARTS=""
[ -f "${ROOTFS}/etc/apt/sources.list" ] && SOURCELIST="${ROOTFS}/etc/apt/sources.list"
[ -d "${ROOTFS}/etc/apt/sources.list.d" ] && SOURCEPARTS="${ROOTFS}/etc/apt/sources.list.d"
if [ -z "${SOURCELIST}" ] && [ -z "${SOURCEPARTS}" ]; then
  log "target rootfs at ${ROOTFS} is missing apt sources under /etc/apt"
  exit 3
fi

mkdir -p /tmp/dalec
chmod 0700 /tmp/dalec

mkdir -p \
  "${ROOTFS}/var/lib/dpkg" \
  "${ROOTFS}/var/lib/apt/lists/partial" \
  "${ROOTFS}/var/cache/apt/archives" \
  "${ROOTFS}/var/cache/apt/archives/partial" \
  "${ROOTFS}/usr/bin" \
  "${ROOTFS}/usr/sbin"

if [ ! -f "${ROOTFS}/var/lib/dpkg/status" ]; then
  : > "${ROOTFS}/var/lib/dpkg/status"
fi

# --- dpkg wrappers for APT solver (native) ---
# NOTE: These wrappers must operate on ROOTFS dpkg DB (NOT host), otherwise
# apt will solve against the wrong installed-set/arch.
cat > /tmp/dalec/dpkg <<'EOF'
#!/bin/sh
set -e
ROOTFS="${DALEC_ROOTFS}"
ADMINDIR="${ROOTFS}/var/lib/dpkg"
ARCH="${DALEC_TARGET_ARCH}"
case "${1:-}" in
  --print-architecture) echo "${ARCH}"; exit 0 ;;
  --print-foreign-architectures) exit 0 ;;
  --add-architecture) exit 0 ;;
esac

exec /usr/bin/dpkg \
  --root="${ROOTFS}" \
  --admindir="${ADMINDIR}" \
  --force-architecture \
  "$@"
EOF
chmod +x /tmp/dalec/dpkg

cat > /tmp/dalec/dpkg-query <<'EOF'
#!/bin/sh
set -e
ROOTFS="${DALEC_ROOTFS}"
ADMINDIR="${ROOTFS}/var/lib/dpkg"
exec /usr/bin/dpkg-query \
  --root="${ROOTFS}" \
  --admindir="${ADMINDIR}" \
  "$@"
EOF
chmod +x /tmp/dalec/dpkg-query

# Host-side arch-scoped APT caches (speed + avoid collisions)
ARCHIVE_DIR="/var/cache/apt/archives-${ARCH}"
LISTS_DIR="/var/cache/apt/lists-${ARCH}"
mkdir -p "${ARCHIVE_DIR}/partial" "${LISTS_DIR}/partial"
rm -rf "${LISTS_DIR}/"* 2>/dev/null || true
mkdir -p "${LISTS_DIR}/partial"

APT_OPTS="
 -o Dir::State=${ROOTFS}/var/lib/apt
 -o Dir::State::Lists=${LISTS_DIR}
 -o Dir::State::status=${ROOTFS}/var/lib/dpkg/status
 -o Dir::Cache=/var/cache/apt
 -o Dir::Cache::archives=${ARCHIVE_DIR}
 -o APT::Architecture=${ARCH}
 -o APT::Architectures::=${ARCH}
 -o APT::Architectures::=all
 -o Dir::Bin::dpkg=/tmp/dalec/dpkg
 -o Dir::Bin::dpkg-query=/tmp/dalec/dpkg-query
 -o Acquire::Languages=none
 -o Dpkg::Use-Pty=0
"
if [ -n "${SOURCELIST}" ]; then
  APT_OPTS="${APT_OPTS} -o Dir::Etc::sourcelist=${SOURCELIST}"
fi
if [ -n "${SOURCEPARTS}" ]; then
  APT_OPTS="${APT_OPTS} -o Dir::Etc::sourceparts=${SOURCEPARTS}"
fi

apt-get ${APT_OPTS} update

DEBIAN_FRONTEND=noninteractive apt-get ${APT_OPTS} --download-only install -y "$@"

# We must have target-arch and/or arch-independent .debs downloaded.
if ! ls "${ARCHIVE_DIR}"/*_"${ARCH}".deb "${ARCHIVE_DIR}"/*_all.deb >/dev/null 2>&1; then
  log "no downloaded debs found in ${ARCHIVE_DIR} for arch=${ARCH}"
  exit 6
fi

# Prevent daemons from starting during maintainer scripts.
POLICY="${ROOTFS}/usr/sbin/policy-rc.d"
cat > "${POLICY}" <<'EOF'
#!/bin/sh
exit 101
EOF
chmod +x "${POLICY}"
trap 'rm -f "${POLICY}" 2>/dev/null || true; cleanup_apt_proxy' EXIT

chroot_configure() { chroot "${ROOTFS}" /usr/bin/dpkg --configure -a; }

export DEBIAN_FRONTEND=noninteractive
export DEBCONF_NONINTERACTIVE_SEEN=true
export DEBIAN_PRIORITY=critical
export NEEDRESTART_MODE=a

# Phase 2a: UNPACK (native, outside chroot) into mounted ROOTFS using dpkg --root.
# This is the fast path: avoids running unpack under QEMU.
for deb in "${ARCHIVE_DIR}"/*_"${ARCH}".deb "${ARCHIVE_DIR}"/*_all.deb; do
	[ -f "${deb}" ] || continue
	/tmp/dalec/dpkg --unpack --force-depends --no-triggers "$deb"
done

# Phase 2b: configure inside target rootfs.
chroot_configure

cnt="$(awk '/^Package: /{n++} END{print n+0}' "${ROOTFS}/var/lib/dpkg/status" 2>/dev/null || echo 0)"
log "ROOTFS dpkg status packages: ${cnt}"
`
		script := llb.Scratch().File(
			llb.Mkfile("install-into-root.sh", 0o755, []byte(installScript)),
			dalec.WithConstraint(&ei.Constraints),
		)

		p := "/tmp/dalec/internal/deb/install-into-root.sh"
		llb.AddMount(p, script, llb.SourcePath("install-into-root.sh")).SetRunOption(ei)
		llb.AddEnv("DEBIAN_FRONTEND", "noninteractive").SetRunOption(ei)
		llb.AddEnv("DALEC_ROOTFS", rootfsPath).SetRunOption(ei)
		llb.AddEnv("DALEC_TARGET_ARCH", targetArch).SetRunOption(ei)
		llb.Args(append([]string{p}, packages...)).SetRunOption(ei)
	})
}

// InstallLocalPkg installs all deb packages found in the root of the provided [llb.State]
//
// In some cases, with strict version constraints in the package's dependencies,
// this will use `aptitude` to help resolve those dependencies since apt is
// currently unable to handle strict constraints.
//
// This returns an [llb.RunOption] but it does create some things internally,
// This is what the constraints opts are used for.
// The constraints are applied after any constraint set on the [llb.ExecInfo]
func InstallLocalPkg(pkg llb.State, upgrade bool, opts ...llb.ConstraintsOpt) llb.RunOption {
	return dalec.RunOptFunc(func(ei *llb.ExecInfo) {
		// The apt solver always tries to select the latest package version even
		// when constraints specify that an older version should be installed and
		// that older version is available in a repo. This leads the solver to
		// simply refuse to install our target package if the latest version of ANY
		// dependency package is incompatible with the constraints. To work around
		// this we first install the .deb for the package with dpkg, specifically
		// ignoring any dependencies so that we can avoid the constraints issue.
		// We then use aptitude to fix the (possibly broken) install of the
		// package, and we pass the aptitude solver a hint to REJECT any solution
		// that involves uninstalling the package. This forces aptitude to find a
		// solution that will respect the constraints even if the solution involves
		// pinning dependency packages to older versions.
		const installScript = `#!/usr/bin/env sh
	set -ex
	` + aptProxyConfigScript + `

	configure_apt_proxy
	trap cleanup_apt_proxy EXIT

	# Make sure any cached data from local repos is purged since this should not
	# be shared between builds.
rm -f /var/lib/apt/lists/_*
apt autoclean -y

# Remove any previously failed attempts to get repo data
rm -rf /var/lib/apt/lists/partial/*
apt update

if [ "${DALEC_UPGRADE}" = "true" ]; then
	apt dist-upgrade -y
fi

if apt install -y ${1}; then
	exit 0
fi

if ! command -v aptitude > /dev/null; then
	needs_cleanup=1
	apt install -y aptitude
fi

cleanup() {
	exit_code=$?
	cleanup_apt_proxy
	if [ "${needs_cleanup}" = "1" ]; then
		apt remove -y aptitude
	fi
	exit $exit_code
}

trap cleanup EXIT

dpkg -i --force-depends ${1}

pkg_name="$(dpkg-deb -f ${1} | grep 'Package:' | awk -F': ' '{ print $2 }')"
aptitude install -y -f -o "Aptitude::ProblemResolver::Hints::=reject ${pkg_name} :UNINST"
`

		script := llb.Scratch().File(
			llb.Mkfile("install.sh", 0o755, []byte(installScript)),
			dalec.WithConstraint(&ei.Constraints),
			dalec.WithConstraints(opts...),
		)

		p := "/tmp/dalec/internal/deb/install-with-constraints.sh"
		debPath := "/tmp/dalec/internal/debs"

		llb.AddMount(p, script, llb.SourcePath("install.sh")).SetRunOption(ei)
		llb.AddMount(debPath, pkg, llb.Readonly).SetRunOption(ei)
		llb.AddEnv("DEBIAN_FRONTEND", "noninteractive").SetRunOption(ei)
		llb.AddEnv("DALEC_UPGRADE", strconv.FormatBool(upgrade)).SetRunOption(ei)

		args := []string{p, filepath.Join(debPath, "*.deb")}
		llb.Args(args).SetRunOption(ei)
	})
}

func (d *Config) InstallBuildDeps(ctx context.Context, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		deps := spec.GetPackageDeps(targetKey).GetBuild()
		if len(deps) == 0 {
			return in
		}

		depsSpec := &dalec.Spec{
			Name:     spec.Name + "-build-deps",
			Packager: "Dalec",
			Version:  spec.Version,
			Revision: spec.Revision,
			Dependencies: &dalec.PackageDependencies{
				Runtime: deps,
			},
			Description: "Build dependencies for " + spec.Name,
		}

		opts := append(opts, dalec.ProgressGroup("Install build dependencies"))
		debRoot := deb.Debroot(ctx, sOpt, depsSpec, in, llb.Scratch(), targetKey, "", d.VersionID, deb.SourcePkgConfig{}, opts...)

		pkg := deb.BuildDebBinaryOnly(in, depsSpec, debRoot, "", opts...)

		repos := dalec.GetExtraRepos(d.ExtraRepos, "build")
		repos = append(repos, spec.GetBuildRepos(targetKey)...)

		customRepos := d.RepoMounts(repos, sOpt, opts...)

		return in.Run(
			dalec.WithConstraints(opts...),
			customRepos,
			InstallLocalPkg(pkg, false, opts...),
			aptProxyConfig(sOpt),
			dalec.WithMountedAptCache(d.AptCachePrefix, opts...),
			deps.GetSourceLocation(in),
		).Root()
	}
}

func (d *Config) InstallTestDeps(sOpt dalec.SourceOpts, targetKey string, spec *dalec.Spec, opts ...llb.ConstraintsOpt) llb.StateOption {
	deps := spec.GetPackageDeps(targetKey).GetTest()
	if len(deps) == 0 {
		return func(s llb.State) llb.State { return s }
	}

	return func(in llb.State) llb.State {
		repos := dalec.GetExtraRepos(d.ExtraRepos, "test")
		repos = append(repos, spec.GetTestRepos(targetKey)...)

		withRepos := d.RepoMounts(repos, sOpt, opts...)

		opts = append(opts, dalec.ProgressGroup("Install test dependencies"))
		return in.Run(
			dalec.WithConstraints(opts...),
			AptInstall(dalec.SortMapKeys(deps), opts...),
			aptProxyConfig(sOpt),
			withRepos,
			dalec.WithMountedAptCache(d.AptCachePrefix, opts...),
			deps.GetSourceLocation(in),
		).Root()
	}
}

func (d *Config) DownloadDeps(worker llb.State, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, constraints dalec.PackageDependencyList, opts ...llb.ConstraintsOpt) llb.State {
	if constraints == nil {
		return llb.Scratch()
	}

	opts = append(opts, dalec.ProgressGroup("Downloading dependencies"))

	scriptPath := "/tmp/dalec/internal/deb/download.sh"
	const scriptSrc = `#!/usr/bin/env bash
	set -euxo pipefail
	cd /output

	` + aptProxyConfigScript + `

	configure_apt_proxy
	trap cleanup_apt_proxy EXIT

	# Make sure any cached data from local repos is purged since this should not
# be shared between builds.
rm -f /var/lib/apt/lists/_*
apt autoclean -y
apt update

# Use APT to resolve the constraints and download just the requested packages
# without the sub-dependencies. Ideally, we would resolve all the constraints
# together and match the packages by name, but the resolved name is often
# different. We therefore have to resolve each constraint in turn and assume
# that the last Inst line corresponds to the requested package. This should be
# the case when recommends and suggests are omitted.
for CONSTRAINT; do
	apt satisfy -y -s --no-install-recommends --no-install-suggests "${CONSTRAINT}" |
		tac |
		sed -n -r 's/^Inst ([^ ]+) \(([^ ]+).*/\1=\2/p;T;q' |
		xargs -t apt download
done
`

	scriptFile := llb.Scratch().File(
		llb.Mkfile("download.sh", 0o755, []byte(scriptSrc)),
		dalec.WithConstraints(opts...),
	)

	return worker.Run(
		llb.Args(append([]string{scriptPath}, deb.AppendConstraints(constraints)...)),
		llb.AddMount(scriptPath, scriptFile, llb.SourcePath("download.sh"), llb.Readonly),
		llb.AddMount("/var/lib/dpkg", llb.Scratch(), llb.Tmpfs()),
		llb.AddEnv("DEBIAN_FRONTEND", "noninteractive"),
		aptProxyConfig(sOpt),
		dalec.WithMountedAptCache(d.AptCachePrefix, opts...),
		dalec.WithConstraints(opts...),
	).AddMount("/output", llb.Scratch())
}
