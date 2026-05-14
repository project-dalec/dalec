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
		return bootstrapContainer(input)
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
					//
					// Licenses also install under /usr/share/doc on deb targets, so the
					// excludes workaround is equally required when the spec has license
					// artifacts (even if it has no docs or manpages).
					artifacts := input.Spec.GetArtifacts(input.Target)
					if !artifacts.HasDocs() && len(artifacts.Licenses) == 0 {
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

func bootstrapContainer(input buildContainerInput) llb.State {
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

# Local spec-built .deb files. Passing these by path to apt-get (apt 1.1+
# syntax — available on every distro dalec supports) lets apt parse the
# control files itself and resolve dependencies natively, including:
#   - Pre-Depends (in addition to Depends)
#   - alternatives ("pkg-a | pkg-b") — apt picks an installable option
#   - virtual packages (Provides) — apt picks a real provider
#   - version constraints — unsatisfiable ones cause the build to fail
#   - architecture restrictions
local_package_files=$(ls /spec-packages/*.deb)

# Get the exact filenames apt needs by using --print-uris with an empty cache dir.
# This forces apt to report ALL needed packages (not just uncached ones), giving
# us exact filenames including correct version and architecture suffixes.
# --print-uris output format: 'URL' filename size hash
# We extract the second field (the filename).
#
# Local .deb paths are recognized as already-available and don't appear in
# --print-uris output, so the filenames here are only remote deps.
needed_filenames=$(apt-get -o Dir::State::status="${rootfs}/var/lib/dpkg/status" \
    -o Dir::Cache::Archives=/tmp \
    --yes --print-uris install ${essential_packages} ${bootstrap_extra_packages} ${local_package_files} \
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
# Passing the local .deb paths anchors the install plan to the spec packages
# without re-fetching them.
apt-get -o Dir::State::status="${rootfs}/var/lib/dpkg/status" \
    -o Dir::Cache::Archives="${rootfs}${apt_archives}" \
    --yes --download-only install ${essential_packages} ${bootstrap_extra_packages} ${local_package_files}

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
		// 1. Install all .deb files (unpacks + configures). --force-depends
		//    tolerates dependency mis-ordering since dpkg sorts alphabetically.
		// 2. Re-run --configure --pending to finish configuring anything that
		//    was left half-configured by the first pass. This is what builds
		//    out the update-alternatives chains for packages like mawk
		//    (provides /usr/bin/awk via /etc/alternatives/awk -> /usr/bin/mawk).
		//    Without this, dpkg reports mawk as "installed" but the alternatives
		//    chain is empty and /usr/bin/awk does not exist.
		// 3. Remove the staged .deb files.
		llb.Args([]string{
			"/usr/bin/sh", "-c",
			"dpkg --install --force-depends /var/cache/apt/archives/*.deb && " +
				"dpkg --configure --pending --force-depends && " +
				"rm -rf /var/cache/apt/archives/*.deb",
		}),
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

# Append /tmp to PATH so the no-op diff/tar stubs mounted there by the
# Go caller (see stubSt below) are picked up by any maintainer script
# that calls 'diff' or 'tar' without an absolute path AFTER the real
# tools have been purged. These stubs need to be reachable for BOTH
# purge passes (purge_first and purge_last) — the first pass purges
# most of the system and frequently triggers prerm/postrm scripts
# that exec diff or tar; if those tools have already been removed and
# the stubs are not on PATH, the maintainer scripts crash and leave
# packages in an inconsistent state.
#
# IMPORTANT: /tmp is APPENDED, not prepended. dpkg-deb internally
# execs 'tar' to read .deb control archives, so prepending /tmp would
# make every dpkg-deb invocation in this script (including the
# keep_set seeding right below) fail with 'tar subprocess returned
# error exit status 1' and leave us with an empty keep_set.
PATH="${PATH}:/tmp"
export PATH

# Remove problematic maintainer scripts that cause infinite loops during purge.
rm -f /var/lib/dpkg/info/libpam-runtime.prerm 2>/dev/null || true

# Recursive dependency resolver: prints the transitive closure of installed
# Depends/Pre-Depends starting from the given space-separated package list.
#
# Handles three tricky cases that a naive dpkg-query+sed pipeline gets wrong:
#   - Alternatives ("pkg-a | pkg-b"): walk every option, keep whichever is
#     installed (rather than blindly picking the first).
#   - Virtual packages (Provides): if a dep name is not an installed package
#     itself, look up installed packages whose Provides field lists it and
#     keep those instead. Without this, virtual deps like "awk" silently
#     drop their real provider (mawk/gawk) from the keep set.
#   - Multi-arch qualifiers (":amd64"): stripped so the name matches.
resolve_deps() {
    queue="$1"
    resolved=""
    while [ -n "${queue}" ]; do
        pkg=$(echo "${queue}" | head -n1)
        queue=$(echo "${queue}" | tail -n +2)

        if [ -z "${pkg}" ] || echo "${resolved}" | grep -qw "${pkg}"; then continue; fi

        resolved="${resolved} ${pkg}"

        # Strip version constraints "(>= 1.0)", arch restrictions "[amd64]",
        # whitespace, and multi-arch qualifiers ":amd64". Keep alternatives
        # ("|") as-is; they are split below.
        deps=$(dpkg-query -W -f='${Depends}\n${Pre-Depends}\n' "${pkg}" 2>/dev/null \
            | tr ',' '\n' | sed 's/([^)]*)//g; s/\[[^]]*\]//g; s/[[:space:]]//g; s/:[a-z0-9-]*//g' | grep -v '^$' | sort -u)

        for dep_alt in ${deps}; do
            # Walk all alternatives ("pkg-a|pkg-b") so we don't lose track
            # of whichever option is actually installed.
            for dep in $(echo "${dep_alt}" | tr '|' ' '); do
                if [ -z "${dep}" ] || echo "${resolved}" | grep -qw "${dep}"; then
                    continue
                fi

                # Real, directly-installed package.
                if dpkg -s "${dep}" 2>/dev/null | grep -q '^Status: install ok installed'; then
                    queue=$(printf '%s\n%s' "${queue}" "${dep}")
                    continue
                fi

                # Virtual package: find installed packages whose Provides
                # field lists this name (with optional version constraint
                # stripped) and keep them.
                providers=$(dpkg-query -W -f='${Package}|${Provides}\n' 2>/dev/null \
                    | awk -F'|' -v want="${dep}" '
                        $2 == "" { next }
                        {
                            n = split($2, list, ",")
                            for (i = 1; i <= n; i++) {
                                name = list[i]
                                sub(/\(.*$/, "", name)
                                gsub(/^[ \t]+|[ \t]+$/, "", name)
                                if (name == want) { print $1; next }
                            }
                        }
                    ')
                for prov in ${providers}; do
                    if echo "${resolved}" | grep -qw "${prov}"; then continue; fi
                    queue=$(printf '%s\n%s' "${queue}" "${prov}")
                done
            done
        done
    done
    echo "${resolved}"
}

# Packages from the user's spec — the starting point of the keep set.
#
# Seed with TWO sources, then take the transitive closure of both:
#
#   1. The spec package names themselves (read from each .deb's Package
#      field). resolve_deps will walk their dpkg-query Depends and find
#      everything they require post-install.
#
#   2. The Depends + Pre-Depends fields read directly from each spec
#      .deb's control data. This is the critical safety net: if
#      anything goes wrong with the spec package's installed state in
#      dpkg's database (e.g. half-configured, missing entirely from
#      the status DB, or dpkg-query returning empty Depends for any
#      reason), resolve_deps walking only from the package name would
#      drop the user's runtime deps from the keep set and they'd be
#      purged. Reading Depends straight from the .deb sidesteps any
#      installed-state pathologies.
keep_set=""
for f in $(ls /tmp/dalec-spec-packages/*.deb 2>/dev/null); do
    keep_set="${keep_set} $(dpkg-deb -f "${f}" Package)"

    # Pull the runtime Depends + Pre-Depends from the .deb control
    # itself and normalize them the same way resolve_deps normalizes
    # dpkg-query output (strip version constraints, arch restrictions,
    # whitespace, multi-arch qualifiers; keep '|' alternatives so they
    # are split into individual names below).
    raw_deps=$(dpkg-deb -f "${f}" Depends Pre-Depends 2>/dev/null \
        | sed 's/^[A-Za-z-]*: *//' \
        | tr ',' '\n' \
        | sed 's/([^)]*)//g; s/\[[^]]*\]//g; s/[[:space:]]//g; s/:[a-z0-9-]*//g' \
        | grep -v '^$' | sort -u)
    for dep_alt in ${raw_deps}; do
        for dep in $(echo "${dep_alt}" | tr '|' ' '); do
            [ -z "${dep}" ] && continue
            keep_set="${keep_set} ${dep}"
        done
    done
done

# base-files is mandatory and must never be purged. On merged-usr
# distros (Debian 12+/Ubuntu 24.04+) it owns the top-level /bin, /lib,
# /lib64 and /sbin symlinks that point into /usr. Every dynamically
# linked binary in the image resolves its ELF interpreter through one
# of these (e.g. /lib64/ld-linux-x86-64.so.2 -> usr/lib64/...). If
# base-files is purged, dpkg removes those symlinks and EVERY binary —
# including the spec package's runtime deps — fails to exec with
# "no such file or directory" (the kernel's ENOENT for a missing
# interpreter), even though the binaries themselves are still present.
#
# It is not pulled in transitively by typical runtime deps, so seed it
# explicitly here. resolve_deps will also pull in its dependencies.
keep_set="${keep_set} base-files"

# Full transitive closure of the seed set. Cleanup tools end up here
# only if a spec package actually depends on them (directly or
# transitively), in which case we keep them and their deps.
keep_set=$(resolve_deps "$(echo ${keep_set} | tr ' ' '\n')")

# Surface the resolved keep set in build logs for diagnostic purposes.
# A small keep_set (just the spec package itself, no transitive deps)
# is a legitimate outcome for specs that declare no runtime deps and
# whose generated .deb's Depends field is empty — those builds intend
# the cleanup to purge everything except the spec package. We log the
# resolved set so unexpected smallness is at least visible to anyone
# triaging "the binary I expected is missing" issues, but we do NOT
# fail the build here.
echo "DALEC keep_set (resolved): ${keep_set}"

# Persist the keep set so the worker dpkg-remove step (which runs against
# /target using --root=) can validate that every keep-set package is still
# installed after both purge passes. We can't reliably validate this from
# inside the container: purge_last may remove dpkg's own shared libraries
# (libmd, libc, libpam...) before we get to run dpkg-query, producing
# false "missing" verdicts. The worker has its own working dpkg + libs and
# can safely query the target rootfs via --root=/target.
#
# Only the names that correspond to actually-installed packages are
# persisted. The seed includes the raw Depends names (e.g. 'awk') which
# resolve_deps then follows via Provides to a real installed package
# (e.g. 'mawk'); both end up in ${keep_set}, but the worker can only
# audit the latter — dpkg-query against the virtual name 'awk' would
# return 'not-installed' and produce a false-positive validation
# failure even though the build is correct.
mkdir -p /var/lib/dpkg
: > /var/lib/dpkg/.dalec-keep-set
for pkg in ${keep_set}; do
    if dpkg-query -W -f='${db:Status-Status}\n' "${pkg}" 2>/dev/null | grep -qx installed; then
        printf '%s\n' "${pkg}" >> /var/lib/dpkg/.dalec-keep-set
    fi
done

# purge_last: cleanup tools (+ their deps) not in the keep set. These
# survive the main purge so they remain available for it, then get purged
# at the very end.
purge_last=""

# Tools needed by the cleanup process itself (purging packages, running
# maintainer scripts, etc.) but not necessarily wanted in the final image.
# If a spec package transitively depends on any of these, it (and its full
# dependency tree) stays in the keep set; otherwise it gets purged at the end.
#
# findutils provides /usr/bin/find and /usr/bin/xargs, which many
# packages' prerm/postrm scripts shell out to during the first purge
# pass (e.g. libstdc++6's prerm uses 'find … | xargs' to clear ld.so
# cache entries). Purging findutils early causes those scripts to
# exit 127 and leaves their owning packages in a half-removed state.
#
# base-files is intentionally NOT in this list: it is always kept (see
# keep_set seeding above) because it owns the merged-usr root symlinks
# required by every dynamically linked binary in the image.
for pkg in dpkg dash coreutils libc-bin grep findutils; do
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
    # Strip prerm/postrm scripts of packages we're about to purge.
    # Many of them (libpam-modules, libpam0g, anything debconf-aware)
    # unconditionally exec helpers like /usr/share/debconf/frontend
    # that may already have been purged earlier in this same pass —
    # dpkg purges in alphabetical order, not dependency order, so
    # debconf often goes away before its consumers. When that happens,
    # the script returns exit 127, dpkg flags the package as failed,
    # and the worker-side dpkg --audit reports the resulting
    # 'config-files' state as inconsistent and fails the build.
    #
    # Skipping the *rm scripts is safe in this context:
    #   - cleanup_dirs and the final layer squash wipe whatever state
    #     a postrm would have unwound.
    #   - The packages being purged are explicitly not in the final
    #     image, so nothing depends on the unwind side-effects.
    #
    # Multi-arch packages have files named ${pkg}:${arch}.{pre,post}rm,
    # which is why we use a shell glob in addition to the bare name.
    for pkg in ${purge_first}; do
        rm -f "/var/lib/dpkg/info/${pkg}.prerm" \
              "/var/lib/dpkg/info/${pkg}.postrm" 2>/dev/null || true
        for f in /var/lib/dpkg/info/${pkg}:*.prerm \
                 /var/lib/dpkg/info/${pkg}:*.postrm; do
            [ -e "${f}" ] && rm -f "${f}"
        done
    done

    dpkg --purge --force-depends --force-remove-essential ${purge_first} || true
fi

# Remove leftover directories (after dpkg purge so maintainer scripts still work).
cleanup_dirs="
/etc/apt
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
"

# Preserve /etc/systemd and /var/lib/systemd if the final image actually
# uses systemd. Otherwise, these are dead config / state directories that
# can be pruned.
#
# We check the dpkg status here (after purge_first has run) rather than
# only looking at keep_set, because keep_set tracks names from the spec
# .deb but systemd may also have arrived via a base-image dependency,
# Provides, or by being pulled in as a Recommends/Suggests.
#
# Detection covers three signals, in order:
#   1. The systemd PID-1 binary itself is on disk. This is the most
#      reliable indicator because dpkg-deb --extract unpacks files
#      regardless of whether postinst later succeeds. In stripped /
#      minimal containers systemd's postinst frequently fails (no init,
#      no D-Bus, can't enable units), leaving the package in
#      'half-configured' state — but the binary is present and
#      /etc/systemd still matters.
#
#      We deliberately do NOT check for /usr/lib/systemd/system as a
#      proxy: many non-systemd packages (e2fsprogs, init-system-helpers,
#      dbus, etc.) ship unit files there, so its presence does not imply
#      that systemd itself is installed.
#   2. dpkg-query reports any installed-ish state for the systemd
#      package (covers the half-configured case explicitly, in case the
#      binary lives in a non-standard location).
#   3. systemctl on PATH — pragmatic fallback for custom base images
#      where systemd is set up by other means.
keep_systemd=0
if [ -x /usr/lib/systemd/systemd ] || [ -x /lib/systemd/systemd ]; then
    keep_systemd=1
fi
if [ "${keep_systemd}" != "1" ]; then
    case "$(dpkg-query -W -f='${db:Status-Status}\n' systemd 2>/dev/null)" in
        installed|half-configured|triggers-awaited|triggers-pending)
            keep_systemd=1
            ;;
    esac
fi
if [ "${keep_systemd}" != "1" ] && command -v systemctl >/dev/null 2>&1; then
    keep_systemd=1
fi

if [ "${keep_systemd}" != "1" ]; then
    cleanup_dirs="${cleanup_dirs}
/etc/systemd
/var/lib/systemd
"
fi

if [ "${DALEC_KEEP_USR_SHARE_DOC}" != "true" ]; then
    cleanup_dirs="${cleanup_dirs}
/usr/share/doc
/usr/share/man
/usr/share/info
"
fi

# Policy note: the conditional above is "all-or-nothing" for the entire
# /usr/share/doc, /usr/share/man, /usr/share/info trees. When the spec
# author opts in by declaring docs/manpages/licenses, dalec preserves
# these directories wholesale — which also retains docs and manpages
# shipped by the spec package's RUNTIME DEPENDENCIES. When the spec
# author opts out (no docs/manpages/licenses), the trees are pruned
# wholesale, taking dependency-owned docs and manpages with them.
#
# This is intentional for the minimal-container use case (the primary
# consumer of this code path): users who care about image size want all
# /usr/share/doc and /usr/share/man content gone, and users who declare
# their own docs/licenses are explicitly expressing "I want these paths
# to exist in the final image". A more granular policy (e.g. preserve
# only spec-owned files under those paths) would require either dpkg
# diversions per file or a post-install scan against a known manifest,
# both of which add significant complexity for marginal benefit in the
# minimal-image scenario.
#
# Spec authors who want to retain dependency-owned docs/manpages can
# declare any docs/manpages/licenses of their own to flip the toggle, or
# use the non-minimal container target where this cleanup does not run
# at all.

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
    dpkg --purge --force-depends --force-remove-essential ${force_remove_protected} ${purge_last} || true
fi

# Note: /var/log is intentionally NOT cleaned here. dpkg purges above
# write to /var/log/dpkg.log, and the subsequent worker dpkg-remove step
# may add more entries. The worker performs the final /var/log emptying
# at the very end so all log writes are captured.
`

	// Script that runs on the worker to (a) optionally remove dpkg from the
	// target rootfs and (b) validate the target's dpkg database after the
	// in-container cleanup. Using --root= lets the worker's own dpkg binary
	// operate on the mounted rootfs without depending on the target's own
	// (possibly half-removed) dpkg/libraries.
	dpkgRemoveScript := `#!/bin/sh
set -x

keep_set_file=/target/var/lib/dpkg/.dalec-keep-set

# --force-remove-protected was added in dpkg 1.20.6; older releases don't
# recognize it. The worker's dpkg may differ from the target's, so probe it.
force_remove_protected=""
if dpkg --force-help 2>/dev/null | grep -qw remove-protected; then
    force_remove_protected="--force-remove-protected"
fi

# If the in-container cleanup signalled that dpkg should be removed, do so
# from the worker side using --root=/target. dpkg cannot purge itself from
# inside the container, hence this external step.
if [ -f /target/var/lib/dpkg/.dalec-remove-dpkg ]; then
    rm -f /target/var/lib/dpkg/.dalec-remove-dpkg

    # Remove dpkg and any leftover packages from the target rootfs using
    # the worker's dpkg binary. Use --purge to clean config-files entries
    # too. /var/lib/dpkg/status is preserved because dpkg only removes
    # files it owns, not the status database itself.
    for pkg in $(dpkg --root=/target -l 2>/dev/null | awk '/^[irpu]/ && !/^ii/ {print $2}' || true); do
        dpkg --root=/target --purge --force-depends --force-remove-essential ${force_remove_protected} "${pkg}" 2>/dev/null || true
    done
    if dpkg --root=/target -s dpkg 2>/dev/null | grep -q '^Status:.*installed'; then
        dpkg --root=/target --purge --force-depends --force-remove-essential dpkg || true
    fi
else
    echo "dpkg is a runtime dependency, skipping removal"
fi

# Validate the target's dpkg database now that all purges (both
# in-container and worker-side) are done. The in-container cleanup uses
# --force-depends and tolerates per-package purge failures because
# individual maintainer scripts often can't run in a stripped container.
# What we cannot tolerate is the build succeeding while:
#   - dpkg --audit reports any package in an inconsistent state, or
#   - any keep-set package ended up half-removed / config-failed / missing.
# Running these checks from the worker via --root=/target is reliable
# even when the target's own dpkg/libraries have just been removed.
audit_output=$(dpkg --root=/target --audit 2>/dev/null || true)
if [ -n "${audit_output}" ]; then
    echo "ERROR: dpkg --audit reported inconsistent state in target rootfs:" >&2
    echo "${audit_output}" >&2
    exit 1
fi

if [ -f "${keep_set_file}" ]; then
    while read -r pkg; do
        [ -z "${pkg}" ] && continue
        # Use --admindir rather than --root for the query. dpkg-query's
        # --root option is comparatively recent and is unsupported on
        # older dpkg (e.g. the dpkg-query shipped with Ubuntu 18.04 /
        # Bionic, 1.19.x), where passing --root makes dpkg-query exit
        # non-zero and the '|| echo missing' fallback fires — a false
        # negative that fails the build even though the package is fine.
        # --admindir=<root>/var/lib/dpkg has been supported for far
        # longer and reads the same status database.
        status=$(dpkg-query --admindir=/target/var/lib/dpkg -W -f='${db:Status-Status}\n' "${pkg}" 2>/dev/null || echo "missing")
        case "${status}" in
            installed) ;;
            *)
                echo "ERROR: keep-set package '${pkg}' is in state '${status}' after cleanup (expected 'installed')" >&2
                exit 1
                ;;
        esac
    done < "${keep_set_file}"

    # Remove the marker file so it does not leak into the final image.
    rm -f "${keep_set_file}"
fi

# Empty /target/var/log but keep the directory itself. Many packages and
# runtime processes (logrotate, journald, syslog, libc's openlog(),
# application log files, etc.) expect /var/log to exist and will fail or
# crash if it is missing entirely. Removing only the contents keeps the
# disk savings while preserving the well-known mount/log point.
#
# This runs LAST — after both the in-container cleanup script's purges
# and the worker's optional dpkg-removal purges above. Both write to
# /target/var/log/dpkg.log, /target/var/log/apt/, etc., so emptying
# earlier would just see them repopulated.
if [ -d /target/var/log ]; then
    find /target/var/log -mindepth 1 -delete 2>/dev/null || true
fi
`

	scriptSt := llb.Scratch().File(llb.Mkfile("cleanup.sh", 0o755, []byte(script)), cleanupOpts...)
	dpkgRemoveScriptSt := llb.Scratch().File(llb.Mkfile("dpkg-remove.sh", 0o755, []byte(dpkgRemoveScript)), cleanupOpts...)

	// No-op stub mounted at /tmp/diff and /tmp/tar so dpkg's maintainer scripts
	// find the binaries they expect (diff, tar) without writing to the rootfs.
	stubSt := llb.Scratch().File(llb.Mkfile("stub", 0o755, []byte("#!/bin/sh\nexit 1\n")), cleanupOpts...)

	// Run the main cleanup inside the container (purges everything except dpkg).
	//
	// DALEC_KEEP_USR_SHARE_DOC drives whether /usr/share/doc, /usr/share/man,
	// and /usr/share/info are preserved. On deb targets, dalec installs
	// license artifacts under /usr/share/doc/<pkg>/, so we must preserve
	// those paths whenever the spec has docs, manpages, OR licenses.
	artifacts := input.Spec.GetArtifacts(input.Target)
	keepUsrShareDoc := artifacts.HasDocs() || len(artifacts.Licenses) > 0

	st = st.Run(
		dalec.WithConstraints(cleanupOpts...),
		llb.AddMount("/tmp/dalec-cleanup.sh", scriptSt, llb.SourcePath("cleanup.sh"), llb.Readonly),
		llb.AddMount("/tmp/dalec-spec-packages", input.SpecPackages, llb.Readonly),
		llb.AddMount("/tmp/diff", stubSt, llb.SourcePath("stub"), llb.Readonly),
		llb.AddMount("/tmp/tar", stubSt, llb.SourcePath("stub"), llb.Readonly),
		llb.AddEnv("DALEC_KEEP_USR_SHARE_DOC", strconv.FormatBool(keepUsrShareDoc)),
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
