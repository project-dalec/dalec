package distro

import (
	"context"
	"path/filepath"
	"strconv"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/packaging/linux/deb"
)

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
//
// This is used for cross builds to avoid executing package installs under QEMU:
// we mount the target-arch rootfs and direct apt/dpkg to operate on that filesystem.
//
// Notes:
//   - This relies on the target rootfs containing valid /etc/apt and /var/lib/dpkg state.
//   - Maintainer scripts execute inside the target rootfs. If they invoke target-arch binaries,
//     they will require emulation (binfmt/qemu) on the build host.
//     This still avoids running the entire apt install pipeline under emulation: dependency
//     resolution/download/unpack are native; only maintainer scripts may be emulated.
func AptInstallIntoRoot(rootfsPath string, packages []string, targetArch string, opts ...llb.ConstraintsOpt) llb.RunOption {
	return dalec.RunOptFunc(func(ei *llb.ExecInfo) {
		const installScript = `#!/usr/bin/env sh
set -ex

ROOTFS="${DALEC_ROOTFS}"
ARCH="${DALEC_TARGET_ARCH}"

if [ -z "${ROOTFS}" ] || [ -z "${ARCH}" ]; then
	echo "DALEC_ROOTFS and DALEC_TARGET_ARCH must be set" >&2
	exit 2
fi

if [ -f "${ROOTFS}/var/lib/dpkg/arch" ]; then
	native_arch="$(head -n1 "${ROOTFS}/var/lib/dpkg/arch" | tr -d '\n' || true)"
	if [ -n "${native_arch}" ] && [ "${native_arch}" != "${ARCH}" ]; then
		echo "target rootfs native dpkg arch (${native_arch}) != requested (${ARCH})" >&2
		echo "dpkg arch file (${ROOTFS}/var/lib/dpkg/arch):" >&2
		sed -n '1,40p' "${ROOTFS}/var/lib/dpkg/arch" >&2 || true
		echo "If this is amd64, your target base rootfs mount isn't arm64." >&2
		exit 4
	fi
fi

# Keep host cache (usually mounted) for downloads, but make apt/dpkg operate on ROOTFS state.
# Sanity check: must be a real Debian/Ubuntu rootfs
if [ ! -e "${ROOTFS}/etc/os-release" ] && [ ! -e "${ROOTFS}/usr/lib/os-release" ]; then
  echo "target rootfs at ${ROOTFS} does not look valid (missing os-release)" >&2
  ls -la "${ROOTFS}" >&2 || true
  exit 3
fi
# Ensure required directories exist in the target rootfs
mkdir -p "${ROOTFS}/var/lib/apt/lists/partial" \
         "${ROOTFS}/var/cache/apt/archives/partial" \
         "${ROOTFS}/var/lib/dpkg"

# dpkg status may not exist in some minimal images; create it so apt is happy
if [ ! -e "${ROOTFS}/var/lib/dpkg/status" ]; then
	touch "${ROOTFS}/var/lib/dpkg/status"
fi

rm -f "${ROOTFS}/var/lib/apt/lists/"_*
rm -rf "${ROOTFS}/var/lib/apt/lists/partial/"*

# Preflight: ensure we can execute the target rootfs shell.
# If binfmt/qemu isn't configured, dpkg maintainer scripts will fail later with "Exec format error".
mkdir -p /tmp/dalec
if ! chroot "${ROOTFS}" /bin/sh -c 'true' >/dev/null 2>/tmp/dalec/chroot-test.err; then
	echo "cannot execute target rootfs /bin/sh under ${ROOTFS}" >&2
	echo "this cross-install requires binfmt/qemu for target arch (DALEC_TARGET_ARCH=${ARCH})" >&2
	cat /tmp/dalec/chroot-test.err >&2 || true
	exit 5
fi

# IMPORTANT:
# apt's dependency solver consults dpkg/dpkg-query. During cross builds, those
# binaries are build-arch (amd64) and will otherwise report the wrong arch.
# Override with wrappers that:
#   - operate on the TARGET rootfs dpkg db
#   - report ${ARCH} for --print-architecture

cat > /tmp/dalec/dpkg <<'EOF'
#!/usr/bin/env sh
set -e
ROOTFS="${DALEC_ROOTFS}"
ARCH="${DALEC_TARGET_ARCH}"
ADMINDIR="${ROOTFS}/var/lib/dpkg"

case "${1:-}" in
  --print-architecture) echo "${ARCH}"; exit 0 ;;
  --print-foreign-architectures) exit 0 ;;
esac

exec /usr/bin/dpkg \
  --root="${ROOTFS}" \
  --admindir="${ADMINDIR}" \
  --force-architecture \
  "$@"
EOF
chmod +x /tmp/dalec/dpkg

cat > /tmp/dalec/dpkg-query <<'EOF'
#!/usr/bin/env sh
set -e
ROOTFS="${DALEC_ROOTFS}"
ADMINDIR="${ROOTFS}/var/lib/dpkg"
exec /usr/bin/dpkg-query --root="${ROOTFS}" --admindir="${ADMINDIR}" "$@"
EOF
chmod +x /tmp/dalec/dpkg-query


APT_OPTS="
 -o Dir::State=${ROOTFS}/var/lib/apt
 -o Dir::State::Lists=${ROOTFS}/var/lib/apt/lists
 -o Dir::State::status=${ROOTFS}/var/lib/dpkg/status
 -o Dir::Cache=/var/cache/apt
 -o Dir::Cache::archives=/var/cache/apt/archives
 -o APT::Architecture=${ARCH}
 -o APT::Architectures::=${ARCH}
 -o APT::Architectures::=all
 -o Dir::Bin::dpkg=/tmp/dalec/dpkg
 -o Dir::Bin::dpkg-query=/tmp/dalec/dpkg-query
 -o DPkg::Options::=--root=${ROOTFS}
 -o DPkg::Options::=--admindir=${ROOTFS}/var/lib/dpkg
 -o DPkg::Options::=--force-architecture
"

# Prefer target rootfs apt config if present (keeps sources/keys consistent with the mounted OS).
if [ -d "${ROOTFS}/etc/apt" ]; then
	APT_OPTS="${APT_OPTS}
 -o Dir::Etc::sourcelist=${ROOTFS}/etc/apt/sources.list
 -o Dir::Etc::sourceparts=${ROOTFS}/etc/apt/sources.list.d
 -o Dir::Etc::trustedparts=${ROOTFS}/etc/apt/trusted.gpg.d
"
fi

apt-get ${APT_OPTS} update
if ! DEBIAN_FRONTEND=noninteractive apt-get ${APT_OPTS} install -y "$@"; then
	echo "apt install failed; attempting fix-broken and retrying" >&2
	DEBIAN_FRONTEND=noninteractive apt-get ${APT_OPTS} -f install -y
	DEBIAN_FRONTEND=noninteractive apt-get ${APT_OPTS} install -y "$@"
fi
`

		script := llb.Scratch().File(
			llb.Mkfile("install-into-root.sh", 0o755, []byte(installScript)),
			dalec.WithConstraint(&ei.Constraints),
			dalec.WithConstraints(opts...),
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
			dalec.WithMountedAptCache(d.AptCachePrefix),
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
			withRepos,
			dalec.WithMountedAptCache(d.AptCachePrefix),
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
		dalec.WithMountedAptCache(d.AptCachePrefix),
		dalec.WithConstraints(opts...),
	).AddMount("/output", llb.Scratch())
}
