package distro

import (
	"context"
	"strconv"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
	"github.com/project-dalec/dalec/targets"
)

func (c *Config) BuildContainer(ctx context.Context, client gwclient.Client, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, debSt llb.State, opts ...llb.ConstraintsOpt) llb.State {
	opts = append(opts, frontend.IgnoreCache(client), dalec.ProgressGroup("Build Container Image"))

	input := buildContainerInput{
		Config:       c,
		Client:       client,
		Worker:       c.Worker(sOpt, dalec.Platform(sOpt.TargetPlatform), dalec.WithConstraints(opts...)),
		SOpt:         sOpt,
		Spec:         spec,
		Target:       targetKey,
		SpecPackages: debSt,
		Opts:         opts,
	}

	if c.DefaultOutputImage == "" {
		return bootstrapContainer(ctx, input)
	}

	baseImg := baseImageFromSpec(llb.Image(c.DefaultOutputImage, llb.WithMetaResolver(sOpt.Resolver), dalec.WithConstraints(opts...)), input)

	if len(c.BasePackages) > 0 {
		opts = append(opts, dalec.ProgressGroup("Install base image packages"))

		// Update the base image to include the base packages.
		// This may include things that are necessary to even install the debSt package.
		// So this must be done separately from the debSt package.
		baseImg = baseImg.Run(
			dalec.WithConstraints(opts...),
			InstallLocalPkg(basePackages(ctx, input), true, opts...),
			dalec.WithMountedAptCache(c.AptCachePrefix, opts...),
		).Root()
	}

	return baseImg.With(installPackagesInContainer(input, []llb.RunOption{
		dalec.WithMountedAptCache(input.Config.AptCachePrefix, opts...),
		InstallLocalPkg(debSt, true, opts...),
	}))
}

func baseImageFromSpec(baseImg llb.State, input buildContainerInput) llb.State {
	bi, err := input.Spec.GetSingleBase(input.Target)
	if err != nil {
		return dalec.ErrorState(llb.Scratch(), err)
	}

	if bi == nil {
		return baseImg
	}

	return bi.ToState(input.SOpt, input.Opts...)
}

func basePackages(ctx context.Context, input buildContainerInput) llb.State {
	if len(input.Config.BasePackages) == 0 {
		return llb.Scratch()
	}

	// If we have base packages to install, create a meta-package to install them.
	runtimePkgs := make(dalec.PackageDependencyList, len(input.Config.BasePackages))
	for _, pkgName := range input.Config.BasePackages {
		runtimePkgs[pkgName] = dalec.PackageConstraints{}
	}
	basePkgSpec := &dalec.Spec{
		Name:        "dalec-deb-base-packages",
		Packager:    "dalec",
		Description: "Base Packages for Debian-based Distros",
		Version:     "0.1",
		Revision:    "1",
		Dependencies: &dalec.PackageDependencies{
			Runtime: runtimePkgs,
		},
	}

	opts := append(input.Opts, dalec.ProgressGroup("Install base image packages"))

	return input.Config.BuildPkg(ctx, input.Client, input.SOpt, basePkgSpec, input.Target, opts...)
}

type buildContainerInput struct {
	Config       *Config
	Client       gwclient.Client
	Worker       llb.State
	SOpt         dalec.SourceOpts
	Spec         *dalec.Spec
	Target       string
	SpecPackages llb.State
	Opts         []llb.ConstraintsOpt
}

func extraRepos(input buildContainerInput, opts ...llb.ConstraintsOpt) llb.RunOption {
	// Those base repos come from distro configuration.
	repos := dalec.GetExtraRepos(input.Config.ExtraRepos, "install")

	// These are user specified via spec.
	repos = append(repos, input.Spec.GetInstallRepos(input.Target)...)

	return input.Config.RepoMounts(repos, input.SOpt, opts...)
}

func installPackagesInContainer(input buildContainerInput, ro []llb.RunOption) llb.StateOption {
	return func(baseImg llb.State) llb.State {
		opts := append(input.Opts, dalec.ProgressGroup("Install DEB Packages"))

		debug := llb.Scratch().File(llb.Mkfile("debug", 0o644, []byte(`debug=2`)), opts...)

		return baseImg.Run(
			append(ro,
				dalec.WithConstraints(opts...),
				extraRepos(input, opts...),
				// This file makes dpkg give more verbose output which can be useful when things go awry.
				llb.AddMount("/etc/dpkg/dpkg.cfg.d/99-dalec-debug", debug, llb.SourcePath("debug"), llb.Readonly),
				dalec.RunOptFunc(func(cfg *llb.ExecInfo) {
					// Warning: HACK here
					// The base ubuntu image has this `excludes` config file which prevents
					// installation of a lot of things, including doc files.
					// This is mounting over that file with an empty file so that our test suite
					// passes (as it is looking at these files).
					if !input.Spec.GetArtifacts(input.Target).HasDocs() {
						return
					}

					tmp := llb.Scratch().File(llb.Mkfile("tmp", 0o644, nil), opts...)
					llb.AddMount("/etc/dpkg/dpkg.cfg.d/excludes", tmp, llb.SourcePath("tmp")).SetRunOption(cfg)
				}),
				frontend.IgnoreCache(input.Client, targets.IgnoreCacheKeyContainer),
			)...,
		).Root().
			With(dalec.InstallPostSymlinks(input.Spec.GetImagePost(input.Target), input.Worker, opts...))
	}
}

func bootstrapContainer(ctx context.Context, input buildContainerInput) llb.State {
	opts := input.Opts

	baseImgOpts := append(opts, dalec.ProgressGroup("Bootstrap Base Image"))

	baseImg := llb.Scratch().File(llb.Mkdir("/etc", 0o755), baseImgOpts...).
		File(llb.Mkdir("/etc/apt", 0o755), baseImgOpts...).
		File(llb.Mkdir("/etc/apt/apt.conf.d", 0o755), baseImgOpts...).
		File(llb.Mkdir("/etc/apt/preferences.d", 0o755), baseImgOpts...).
		File(llb.Mkdir("/etc/apt/sources.list.d", 0o755), baseImgOpts...).
		File(llb.Mkdir("/var", 0o755), baseImgOpts...).
		File(llb.Mkdir("/var/cache", 0o755), baseImgOpts...).
		File(llb.Mkdir("/var/cache/apt", 0o755), baseImgOpts...).
		File(llb.Mkdir("/var/cache/apt/archives", 0o755), baseImgOpts...).
		File(llb.Mkdir("/var/lib", 0o755), baseImgOpts...).
		File(llb.Mkdir("/var/lib/dpkg", 0o755), baseImgOpts...).
		File(llb.Mkfile("/var/lib/dpkg/status", 0o644, []byte{}), baseImgOpts...)

	installScript := `#!/bin/sh
set -exu

rootfs=/tmp/rootfs
apt_archives=/var/cache/apt/archives

# Make sure any cached data from local repos is purged since this should not
# be shared between builds.
rm -f /var/lib/apt/lists/_*
# autoclean removes cached deb files which are no longer available in any configured repository.
apt autoclean -y

# Remove any previously failed attempts to get repo data
rm -rf /var/lib/apt/lists/partial/*

# Ensure package index is up to date, required when cache is empty.
apt update

# Select essential packages, since those will be used as a base for the image.
#
# We can't use ?essential since some distros we support have too old apt which does not support patterns.
essential_packages=$(dpkg-query -Wf '${Package} ${Essential}\n' | awk '$2 == "yes" {print $1}')

# Extra packages required to run user package maintainer scripts (postinst etc.)
# during dpkg --install. These are not Essential but commonly assumed to exist
# (e.g. useradd/groupadd from passwd). Cleanup will purge them later unless a
# user package depends on them.

# Extra packages, which would normally be in base packages list for each distro release. However, since
# we want to be able to clean them up after installation and after e.g. creation of users and groups in
# the container, we define them here.
bootstrap_extra_packages="passwd"

local_package_files=$(ls /spec-packages/*.deb)

# Get names of local packages so we can exclude them from apt-get install.
local_package_names=$(for f in ${local_package_files}; do dpkg-deb -f "${f}" Package 2>/dev/null; done | sort -u)

# Extract dependencies of local packages, since we need to download those as well.
#
# Spec packages may depend on base packages, so we need to filter to only download remaining packages, since downloading local packages
# would fail.
dependencies_to_download=$(for f in ${local_package_files}; do dpkg-deb -f "${f}" Depends 2>/dev/null; done | tr ',' '\n' | sed 's/([^)]*)//g; s/|.*//; s/ //g' | grep -v '^$' | sort -u | grep -vxF "${local_package_names}" || true)

# Get the exact filenames apt needs by using --print-uris with an empty cache dir.
# This forces apt to report ALL needed packages (not just uncached ones), giving
# us exact filenames including correct version and architecture suffixes.
# --print-uris output format: 'URL' filename size hash
# We extract the second field (the filename).
needed_filenames=$(apt-get -o Dir::State::status="${rootfs}/var/lib/dpkg/status" \
    -o Dir::Cache::Archives=/tmp \
    --yes --print-uris install ${essential_packages} ${bootstrap_extra_packages} ${dependencies_to_download} \
    | grep '\.deb ' | awk '{print $2}')

mkdir -p "${rootfs}${apt_archives}"/partial
cp ${local_package_files} "${rootfs}${apt_archives}"/

# Copy already-cached needed .deb files from the persistent apt cache into the
# rootfs cache. This avoids picking up stale .deb files from previous unrelated
# builds that remain in the persistent cache.
for filename in ${needed_filenames}; do
    if [ -f "${apt_archives}/${filename}" ]; then
        cp "${apt_archives}/${filename}" "${rootfs}${apt_archives}"/
    fi
done

# Download remaining needed packages directly into the rootfs cache.
# apt skips packages already present, so only missing ones are fetched.
apt-get -o Dir::State::status="${rootfs}/var/lib/dpkg/status" \
    -o Dir::Cache::Archives="${rootfs}${apt_archives}" \
    --yes --download-only install ${essential_packages} ${bootstrap_extra_packages} ${dependencies_to_download}

deb_files=$(ls "${rootfs}${apt_archives}"/*.deb)

# Extract all packages into the target rootfs.
#
# Extract base-files first to establish merged-usr symlinks (/bin -> usr/bin, etc.)
# before other packages create those paths as real directories, which would
# cause tar to fail when base-files tries to create the symlinks later.
base_files_package=$(echo "${deb_files}" | tr ' ' '\n' | grep '/base-files_' || true)
for f in ${base_files_package} $(echo "${deb_files}" | tr ' ' '\n' | grep -v '/base-files_'); do
    dpkg-deb --extract "${f}" "${rootfs}"
done

# Fix merged-usr: on Noble+, /bin, /sbin, /lib should be symlinks to usr/bin, usr/sbin, usr/lib
# but dpkg-deb --extract may recreate them as real directories.
#
# This is required so we can actually run shell using target image to re-install packages for running post-install scripts.
for dir in bin sbin lib; do
    if [ -d "${rootfs}/usr/${dir}" ] && [ -d "${rootfs}/${dir}" ] && [ ! -L "${rootfs}/${dir}" ]; then
        cp -a "${rootfs}/${dir}"/* "${rootfs}/usr/${dir}/" 2>/dev/null || true
        rm -rf "${rootfs}/${dir}"
        ln -s "usr/${dir}" "${rootfs}/${dir}"
    fi
done

# dpkg-deb --extract doesn't run postinst scripts, so the /bin/sh symlink
# normally created by update-alternatives is missing. Create it manually.
if [ ! -e "${rootfs}/usr/bin/sh" ] && [ ! -e "${rootfs}/bin/sh" ]; then
    ln -s dash "${rootfs}/usr/bin/sh"
fi

# Remove usrmerge package - our merged-usr fixup above already handles this,
# and usrmerge's postinst fails on overlayfs (which BuildKit uses).
# Create a fake dpkg status entry so dpkg thinks it's installed.
#
# This only runs when usrmerge package is not installed in the base image, since only then the deb file will be downloaded.
for f in $(echo "${deb_files}" | tr ' ' '\n' | grep -E '/(usrmerge|usr-is-merged)_' || true); do
    pkg=$(dpkg-deb -f "${f}" Package)
    ver=$(dpkg-deb -f "${f}" Version)
    arch=$(dpkg-deb -f "${f}" Architecture)
    printf 'Package: %s\nStatus: install ok installed\nVersion: %s\nArchitecture: %s\nDescription: faked by dalec\n\n' "${pkg}" "${ver}" "${arch}" >> "${rootfs}/var/lib/dpkg/status"

    # Remove the deb file so it won't be re-installed.
    rm "${f}"
done
`

	opts = append(opts, dalec.ProgressGroup("Fetch DEB Packages"))

	script := llb.Scratch().File(llb.Mkfile("install.sh", 0o755, []byte(installScript)), opts...)

	// Use worker to download all packages + deps and install into baseImg.
	baseImg = input.Worker.Run(
		dalec.WithConstraints(opts...),
		llb.AddMount("/tmp/install.sh", script, llb.SourcePath("install.sh")),
		llb.AddMount("/spec-packages", input.SpecPackages, llb.Readonly),
		extraRepos(input, opts...),
		dalec.WithMountedAptCache(input.Config.AptCachePrefix, opts...),
		llb.AddEnv("DEBIAN_FRONTEND", "noninteractive"),
		dalec.ShArgs("/tmp/install.sh"),
		frontend.IgnoreCache(input.Client, targets.IgnoreCacheKeyContainer),
	).AddMount("/tmp/rootfs", baseImageFromSpec(baseImg, input))

	result := baseImg.With(installPackagesInContainer(input, []llb.RunOption{
		dalec.ProgressGroup("Install DEB Packages"),
		llb.AddEnv("DEBIAN_FRONTEND", "noninteractive"),
		llb.Args([]string{"/usr/bin/sh", "-c", "dpkg --install --force-depends /var/cache/apt/archives/*.deb && rm -rf /var/cache/apt/archives/*.deb"}),
	}))

	result = cleanupBootstrapContainer(result, input, opts...)

	// Squash all layers into one by copying the final filesystem into a fresh
	// scratch state. Without this, files extracted in the bootstrap layer but
	// removed during cleanup still occupy space in the earlier layer.
	squashOpts := append(opts, dalec.ProgressGroup("Squash container layers"))
	return llb.Scratch().File(llb.Copy(result, "/", "/", &llb.CopyInfo{
		CopyDirContentsOnly: true,
		CreateDestPath:      true,
		AllowWildcard:       true,
	}), squashOpts...)
}

// cleanupBootstrapContainer removes package manager infrastructure, unnecessary
// packages, and caches from the container image.
func cleanupBootstrapContainer(st llb.State, input buildContainerInput, opts ...llb.ConstraintsOpt) llb.State {
	cleanupOpts := append(opts, dalec.ProgressGroup("Cleanup Bootstrap Container"))

	script := `#!/bin/sh

set -x

# Remove problematic maintainer scripts that cause infinite loops during purge.
rm -f /var/lib/dpkg/info/libpam-runtime.prerm 2>/dev/null || true

# Recursive dependency resolver: prints the transitive closure of installed
# Depends/Pre-Depends starting from the given space-separated package list.
resolve_deps() {
    queue="$1"
    resolved=""
    while [ -n "${queue}" ]; do
        pkg=$(echo "${queue}" | head -n1)
        queue=$(echo "${queue}" | tail -n +2)

        if [ -z "${pkg}" ] || echo "${resolved}" | grep -qw "${pkg}"; then continue; fi

        resolved="${resolved} ${pkg}"

        deps=$(dpkg-query -W -f='${Depends}\n${Pre-Depends}\n' "${pkg}" 2>/dev/null \
            | tr ',' '\n' | sed 's/([^)]*)//g; s/|.*//; s/ //g; s/:.*//g' | grep -v '^$' | sort -u)

        for dep in ${deps}; do
            if ! dpkg -s "${dep}" 2>/dev/null | grep -q '^Status: install ok installed'; then
                continue
            fi
            if echo "${resolved}" | grep -qw "${dep}"; then
                continue
            fi
            queue=$(printf '%s\n%s' "${queue}" "${dep}")
        done
    done
    echo "${resolved}"
}

# Packages from the user's spec — the starting point of the keep set.
keep_set=""
for f in $(ls /tmp/dalec-spec-packages/*.deb 2>/dev/null); do
    keep_set="${keep_set} $(dpkg-deb -f "${f}" Package)"
done

# Full transitive closure of spec packages. Cleanup tools end up here only
# if a spec package actually depends on them (directly or transitively),
# in which case we keep them and their deps.
keep_set=$(resolve_deps "$(echo ${keep_set} | tr ' ' '\n')")

# purge_last: cleanup tools (+ their deps) not in the keep set. These
# survive the main purge so they remain available for it, then get purged
# at the very end.
purge_last=""

# Tools needed by the cleanup process itself (purging packages, running
# maintainer scripts, etc.) but not necessarily wanted in the final image.
# If a spec package transitively depends on any of these, it (and its full
# dependency tree) stays in the keep set; otherwise it gets purged at the end.
for pkg in dpkg dash coreutils base-files libc-bin grep; do
    if echo "${keep_set}" | grep -qw "${pkg}"; then continue; fi

    # dpkg can't purge itself from inside the container; signal the worker
    # step to do it from outside instead.
    if [ "${pkg}" = "dpkg" ]; then
        echo > /var/lib/dpkg/.dalec-remove-dpkg
		continue
    fi

	purge_last="${purge_last} ${pkg}"
done
for pkg in $(resolve_deps "$(echo ${purge_last} | tr ' ' '\n')"); do
    if [ "${pkg}" = "dpkg" ]; then continue; fi
    if echo " ${keep_set} " | grep -q " ${pkg} "; then continue; fi
    if echo " ${purge_last} " | grep -q " ${pkg} "; then continue; fi
    purge_last="${purge_last} ${pkg}"
done

# purge_first: everything not in the keep set, purge_last, or dpkg.
# dpkg is kept around for the purge passes and removed by the worker step.
purge_first=""
# Strip :arch suffixes (e.g. libc6:amd64 -> libc6) so names match.
for pkg in $(dpkg-query -W -f='${Package}\n' | sed 's/:.*//g'); do
    if [ "${pkg}" = "dpkg" ]; then continue; fi
    if echo "${keep_set}" | grep -qw "${pkg}"; then continue; fi
    if echo "${purge_last}" | grep -qw "${pkg}"; then continue; fi
    purge_first="${purge_first} ${pkg}"
done

if [ -n "${purge_first}" ]; then
    dpkg --purge --force-depends --force-remove-essential ${purge_first} || true
fi

# Remove leftover directories (after dpkg purge so maintainer scripts still work).
cleanup_dirs="
/etc/apt
/etc/systemd
/usr/lib/apt
/usr/share/bash-completion
/usr/share/bug
/usr/share/debconf
/usr/share/lintian
/usr/share/locale
/var/cache/apt
/var/cache/debconf
/var/lib/apt
/var/lib/pam
/var/lib/systemd
/var/log
"

if [ "${DALEC_HAS_DOCS}" != "true" ]; then
    cleanup_dirs="${cleanup_dirs}
/usr/share/doc
/usr/share/man
/usr/share/info
"
fi

for d in ${cleanup_dirs}; do
    rm -rf "${d}"
done

# Final purge: strip all maintainer scripts first (prevents triggers from
# firing after /bin/sh is gone), then purge the cleanup tools we kept around
# for the main purge. dpkg itself is purged from outside via the worker.
rm -f /var/lib/dpkg/info/*.prerm \
      /var/lib/dpkg/info/*.postrm \
      /var/lib/dpkg/info/*.preinst \
      /var/lib/dpkg/info/*.postinst 2>/dev/null || true

# --force-remove-protected was added in dpkg 1.20.6; older releases (e.g.
# Debian buster, Ubuntu 18.04) don't recognize it and will error out.
force_remove_protected=""
if dpkg --force-help 2>/dev/null | grep -qw remove-protected; then
    force_remove_protected="--force-remove-protected"
fi

if [ -n "${purge_last}" ]; then
    PATH="/tmp:${PATH}" dpkg --purge --force-depends --force-remove-essential ${force_remove_protected} ${purge_last} || true
fi
`

	// Script that runs on the worker to remove dpkg from the target rootfs.
	// Using --root= lets the worker's own dpkg binary operate on the mounted rootfs
	// without needing dpkg to exist inside the target.
	dpkgRemoveScript := `#!/bin/sh
set -x

# Only proceed if the cleanup script signalled that dpkg should be removed.
if [ ! -f /target/var/lib/dpkg/.dalec-remove-dpkg ]; then
    echo "dpkg is a runtime dependency, skipping removal"
    exit 0
fi
rm -f /target/var/lib/dpkg/.dalec-remove-dpkg

# --force-remove-protected was added in dpkg 1.20.6; older releases don't
# recognize it. The worker's dpkg may differ from the target's, so probe it.
force_remove_protected=""
if dpkg --force-help 2>/dev/null | grep -qw remove-protected; then
    force_remove_protected="--force-remove-protected"
fi

# Remove dpkg and any leftover packages from the target rootfs using the
# worker's dpkg binary. Use --purge to clean config-files entries too.
# /var/lib/dpkg/status is preserved because dpkg only removes files it owns,
# not the status database itself.
for pkg in $(dpkg --root=/target -l 2>/dev/null | awk '/^[irpu]/ && !/^ii/ {print $2}' || true); do
    dpkg --root=/target --purge --force-depends --force-remove-essential ${force_remove_protected} "${pkg}" 2>/dev/null || true
done
if dpkg --root=/target -s dpkg 2>/dev/null | grep -q '^Status:.*installed'; then
    dpkg --root=/target --purge --force-depends --force-remove-essential dpkg || true
fi
`

	scriptSt := llb.Scratch().File(llb.Mkfile("cleanup.sh", 0o755, []byte(script)), cleanupOpts...)
	dpkgRemoveScriptSt := llb.Scratch().File(llb.Mkfile("dpkg-remove.sh", 0o755, []byte(dpkgRemoveScript)), cleanupOpts...)

	// No-op stub mounted at /tmp/diff and /tmp/tar so dpkg's maintainer scripts
	// find the binaries they expect (diff, tar) without writing to the rootfs.
	stubSt := llb.Scratch().File(llb.Mkfile("stub", 0o755, []byte("#!/bin/sh\nexit 1\n")), cleanupOpts...)

	// Run the main cleanup inside the container (purges everything except dpkg).
	st = st.Run(
		dalec.WithConstraints(cleanupOpts...),
		llb.AddMount("/tmp/dalec-cleanup.sh", scriptSt, llb.SourcePath("cleanup.sh"), llb.Readonly),
		llb.AddMount("/tmp/dalec-spec-packages", input.SpecPackages, llb.Readonly),
		llb.AddMount("/tmp/diff", stubSt, llb.SourcePath("stub"), llb.Readonly),
		llb.AddMount("/tmp/tar", stubSt, llb.SourcePath("stub"), llb.Readonly),
		llb.AddEnv("DALEC_HAS_DOCS", strconv.FormatBool(input.Spec.GetArtifacts(input.Target).HasDocs())),
		llb.Args([]string{"/usr/bin/sh", "/tmp/dalec-cleanup.sh"}),
	).Root()

	// Use the worker's dpkg to remove dpkg from the target rootfs via --root=.
	// This avoids the chicken-and-egg problem of dpkg removing itself.
	st = input.Worker.Run(
		dalec.WithConstraints(cleanupOpts...),
		llb.AddMount("/tmp/dpkg-remove.sh", dpkgRemoveScriptSt, llb.SourcePath("dpkg-remove.sh"), llb.Readonly),
		llb.Args([]string{"/bin/sh", "/tmp/dpkg-remove.sh"}),
	).AddMount("/target", st)

	return st
}
