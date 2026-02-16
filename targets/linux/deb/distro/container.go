package distro

import (
	"context"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
	"github.com/project-dalec/dalec/targets"
)

func (c *Config) BuildContainer(ctx context.Context, client gwclient.Client, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, debSt llb.State, opts ...llb.ConstraintsOpt) llb.State {
	return buildContainer(ctx, buildContainerInput{
		Config:             c,
		DefaultOutputImage: c.DefaultOutputImage,
		Client:             client,
		Worker:             c.Worker(sOpt, dalec.Platform(sOpt.TargetPlatform), dalec.WithConstraints(opts...)),
		SOpt:               sOpt,
		Spec:               spec,
		Target:             targetKey,
		SpecPackages:       debSt,
		Opts:               opts,
	})
}

type buildContainerInput struct {
	Config             *Config
	DefaultOutputImage string
	Client             gwclient.Client
	Worker             llb.State
	SOpt               dalec.SourceOpts
	Spec               *dalec.Spec
	Target             string
	SpecPackages       llb.State
	Opts               []llb.ConstraintsOpt
}

func buildContainer(ctx context.Context, input buildContainerInput) llb.State {
	opts := append(input.Opts, frontend.IgnoreCache(input.Client), dalec.ProgressGroup("Build Container Image"))

	// Those base repos come from distro configuration.
	repos := dalec.GetExtraRepos(input.Config.ExtraRepos, "install")

	// These are user specified via spec.
	repos = append(repos, input.Spec.GetInstallRepos(input.Target)...)

	withRepos := input.Config.RepoMounts(repos, input.SOpt, opts...)

	basePkg := llb.Scratch()

	if len(input.Config.BasePackages) > 0 {
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

		basePkg = input.Config.BuildPkg(ctx, input.Client, input.SOpt, basePkgSpec, input.Target, opts...)
	}

	baseImg := llb.Scratch()

	if input.DefaultOutputImage != "" {
		baseImg = llb.Image(input.DefaultOutputImage, llb.WithMetaResolver(input.SOpt.Resolver), dalec.WithConstraints(opts...))
	}

	// Allow spec to override the base image entirely
	bi, err := input.Spec.GetSingleBase(input.Target)
	if err != nil {
		return dalec.ErrorState(llb.Scratch(), err)
	}
	if bi != nil {
		baseImg = bi.ToState(input.SOpt, opts...)
	} else {
		opts := append(opts, dalec.ProgressGroup("Bootstrap Base Image"))

		baseImg = baseImg.File(llb.Mkdir("/etc", 0o755), opts...).
			File(llb.Mkdir("/etc/apt", 0o755), opts...).
			File(llb.Mkdir("/etc/apt/apt.conf.d", 0o755), opts...).
			File(llb.Mkdir("/etc/apt/preferences.d", 0o755), opts...).
			File(llb.Mkdir("/etc/apt/sources.list.d", 0o755), opts...).
			File(llb.Mkdir("/var", 0o755), opts...).
			File(llb.Mkdir("/var/cache", 0o755), opts...).
			File(llb.Mkdir("/var/cache/apt", 0o755), opts...).
			File(llb.Mkdir("/var/cache/apt/archives", 0o755), opts...).
			File(llb.Mkdir("/var/lib", 0o755), opts...).
			File(llb.Mkdir("/var/lib/dpkg", 0o755), opts...).
			File(llb.Mkfile("/var/lib/dpkg/status", 0o644, []byte{}), opts...)
	}

	installScript := `#!/bin/sh
set -exu

rootfs=/tmp/rootfs
apt_archives=/var/cache/apt/archives

# Make sure any cached data from local repos is purged since this should not
# be shared between builds.
rm -f /var/lib/apt/lists/_*
apt autoclean -y

# Remove any previously failed attempts to get repo data
rm -rf /var/lib/apt/lists/partial/*
apt update

# Select essential packages, since those will be used as a base for the image.
#
# We can't use ?essential since some distros we support have too old apk which does not support patterns.
essential_packages=$(dpkg-query -Wf '${Package} ${Essential}\n' | awk '$2 == "yes" {print $1}')

local_package_files=$(ls /base-packages/*.deb /spec-packages/*.deb)

# Get names of local packages so we can exclude them from apt-get install.
local_package_names=$(for f in ${local_package_files}; do dpkg-deb -f "${f}" Package 2>/dev/null; done | sort -u)

# Extract dependencies of local packages, since we need to download those as well.
#
# Spec packages may depend on base packages, so we need to filter to only download remaining packages, since downloading local packages
# would fail.
dependencies_to_download=$(for f in ${local_package_files}; do dpkg-deb -f "${f}" Depends 2>/dev/null; done | tr ',' '\n' | sed 's/([^)]*)//g; s/|.*//; s/ //g' | grep -v '^$' | sort -u | grep -vxF "${local_package_names}")

# Get the exact filenames apt needs by using --print-uris with an empty cache dir.
# This forces apt to report ALL needed packages (not just uncached ones), giving
# us exact filenames including correct version and architecture suffixes.
# --print-uris output format: 'URL' filename size hash
# We extract the second field (the filename).
needed_filenames=$(apt-get -o Dir::State::status="${rootfs}/var/lib/dpkg/status" \
    -o Dir::Cache::Archives=/tmp \
    --yes --print-uris install ${essential_packages} ${dependencies_to_download} \
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
    --yes --download-only install ${essential_packages} ${dependencies_to_download}

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

# Copy apt sources from worker into rootfs so the final container can install packages. Do we want that?
# There is no guarantee that the final image will have access to the same sources worker had (e.g. with  mounted repos).
#
# A the moment this is necessary so we can for example install test dependencies without using worker image.
cp -ar /etc/apt/sources.list* "${rootfs}/etc/apt/"
`

	opts = append(opts, dalec.ProgressGroup("Fetch DEB Packages"))

	script := llb.Scratch().File(llb.Mkfile("install.sh", 0o755, []byte(installScript)), opts...)

	// Use worker to download all packages + deps and install into baseImg.
	baseImg = input.Worker.Run(
		dalec.WithConstraints(opts...),
		llb.AddMount("/tmp/install.sh", script, llb.SourcePath("install.sh")),
		llb.AddMount("/base-packages", basePkg, llb.Readonly),
		llb.AddMount("/spec-packages", input.SpecPackages, llb.Readonly),
		withRepos,
		dalec.WithMountedAptCache(input.Config.AptCachePrefix, opts...),
		llb.AddEnv("DEBIAN_FRONTEND", "noninteractive"),
		dalec.ShArgs("/tmp/install.sh"),
		frontend.IgnoreCache(input.Client, targets.IgnoreCacheKeyContainer),
	).AddMount("/tmp/rootfs", baseImg)

	opts = append(opts, dalec.ProgressGroup("Install DEB Packages"))

	debug := llb.Scratch().File(llb.Mkfile("debug", 0o644, []byte(`debug=2`)), opts...)

	// Run dpkg --install to properly configure the packages.
	baseImg = baseImg.Run(
		dalec.WithConstraints(opts...),
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
		llb.AddEnv("DEBIAN_FRONTEND", "noninteractive"),
		llb.Args([]string{"/usr/bin/sh", "-c", "dpkg --install --force-depends /var/cache/apt/archives/*.deb && rm -rf /var/cache/apt/archives/*.deb"}),
		frontend.IgnoreCache(input.Client, targets.IgnoreCacheKeyContainer),
	).Root()

	return baseImg.With(dalec.InstallPostSymlinks(input.Spec.GetImagePost(input.Target), input.Worker, opts...))
}
