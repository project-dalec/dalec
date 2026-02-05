package distro

import (
	"context"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
	"github.com/project-dalec/dalec/targets"
)

type BuildDistrolessContainerInput struct {
	Config *Config
	Client gwclient.Client // Replace with interface.
	Worker llb.State
	SOpt   dalec.SourceOpts
	Spec   *dalec.Spec
	Target string
	DebSt  llb.State // Why is this DebSt?
	Opts   []llb.ConstraintsOpt
}

func BuildDistrolessContainer(ctx context.Context, input BuildDistrolessContainerInput) llb.State {
	opts := append(input.Opts, frontend.IgnoreCache(input.Client), dalec.ProgressGroup("Build Container Image"))

	// Those base repos come from distro configuration.
	repos := dalec.GetExtraRepos(input.Config.ExtraRepos, "install")

	// These are user specified via spec.
	repos = append(repos, input.Spec.GetInstallRepos(input.Target)...)

	withRepos := input.Config.RepoMounts(repos, input.SOpt, opts...)

	// Step 1: Bootstrap base image structure from scratch
	baseImg := llb.Scratch().
		File(llb.Mkdir("/etc", 0o755), opts...).
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

	// Step 2: Build base packages if configured
	var basePkg llb.State
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
	} else {
		basePkg = llb.Scratch()
	}

	// Step 3: Use worker to download all packages + deps and install into baseImg
	// Worker has apt-get, dpkg, etc. while baseImg is just empty directories
	const installScript = `#!/bin/sh
set -ex

# Ensure apt cache directory exists
mkdir -p /var/cache/apt/archives

# Copy local packages (base + spec) to apt cache
cp /base-packages/*.deb /var/cache/apt/archives/
cp /spec-packages/*.deb /var/cache/apt/archives/

apt-get update

# Download essential packages and all dependencies for our local packages
# Get full recursive dependency tree (including already-installed packages)
# Extract dependencies directly from local .deb files (they aren't in apt repo)
local_deps=$(for f in /var/cache/apt/archives/*.deb; do dpkg-deb -f "$f" Depends 2>/dev/null; done | tr ',' '\n' | sed 's/([^)]*)//g' | tr -d ' ' | grep -v '^$' | sort -u)
all_deps=$(apt-cache depends --recurse --no-recommends --no-suggests --no-conflicts --no-breaks --no-replaces --no-enhances \
    $(dpkg-query -Wf '${Package} ${Essential}\n' | awk '$2 == "yes" {print $1}') \
    $local_deps \
    | grep "^\w" | sort -u)
apt-get --yes --download-only --reinstall install $all_deps

# Extract all packages into the target rootfs
for f in /var/cache/apt/archives/*.deb; do
    dpkg-deb --extract "$f" /tmp/rootfs
done

cp /var/cache/apt/archives/*.deb /tmp/rootfs/var/cache/apt/archives/
`

	script := llb.Scratch().File(llb.Mkfile("install.sh", 0o755, []byte(installScript)), opts...)

	baseImg = input.Worker.Run(
		dalec.WithConstraints(opts...),
		llb.AddMount("/tmp/install.sh", script, llb.SourcePath("install.sh")),
		llb.AddMount("/base-packages", basePkg, llb.Readonly),
		llb.AddMount("/spec-packages", input.DebSt, llb.Readonly),
		withRepos,
		llb.AddEnv("DEBIAN_FRONTEND", "noninteractive"),
		dalec.ShArgs("/tmp/install.sh"),
		frontend.IgnoreCache(input.Client, targets.IgnoreCacheKeyContainer),
	).AddMount("/tmp/rootfs", baseImg)

	// Allow spec to override the base image entirely
	bi, err := input.Spec.GetSingleBase(input.Target)
	if err != nil {
		return dalec.ErrorState(llb.Scratch(), err)
	}
	if bi != nil {
		baseImg = bi.ToState(input.SOpt, opts...)
	}

	debug := llb.Scratch().File(llb.Mkfile("debug", 0o644, []byte(`debug=2`)), opts...)
	debugOpt := llb.AddMount("/etc/dpkg/dpkg.cfg.d/99-dalec-debug", debug, llb.SourcePath("debug"), llb.Readonly)

	// Run dpkg --install to properly configure the packages
	// Use /usr/bin/dash explicitly since /bin/sh symlink doesn't exist yet
	baseImg = baseImg.Run(
		dalec.WithConstraints(opts...),
		debugOpt,
		llb.AddEnv("DEBIAN_FRONTEND", "noninteractive"),
		llb.Args([]string{"/usr/bin/sh", "-c", "dpkg --install --force-depends /var/cache/apt/archives/*.deb && rm -rf /var/cache/apt/archives/*.deb"}),
		frontend.IgnoreCache(input.Client, targets.IgnoreCacheKeyContainer),
	).Root()

	return baseImg.With(dalec.InstallPostSymlinks(input.Spec.GetImagePost(input.Target), input.Worker, opts...))
}

func (c *Config) BuildContainer(ctx context.Context, client gwclient.Client, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, debSt llb.State, opts ...llb.ConstraintsOpt) llb.State {
	if true {
		return BuildDistrolessContainer(ctx, BuildDistrolessContainerInput{
			Config: c,
			Client: client,
			Worker: c.Worker(sOpt, dalec.Platform(sOpt.TargetPlatform), dalec.WithConstraints(opts...)),
			SOpt:   sOpt,
			Spec:   spec,
			Target: targetKey,
			DebSt:  debSt,
			Opts:   opts,
		})
	}

	opts = append(opts, frontend.IgnoreCache(client), dalec.ProgressGroup("Build Container Image"))

	baseImg := llb.Image(c.DefaultOutputImage, llb.WithMetaResolver(sOpt.Resolver), dalec.WithConstraints(opts...))

	bi, err := spec.GetSingleBase(targetKey)
	if err != nil {
		return dalec.ErrorState(llb.Scratch(), err)
	}

	if bi != nil {
		baseImg = bi.ToState(sOpt, opts...)
	}

	// Those base repos come from distro configuration.
	repos := dalec.GetExtraRepos(c.ExtraRepos, "install")

	// These are user specified via spec.
	repos = append(repos, spec.GetInstallRepos(targetKey)...)

	withRepos := c.RepoMounts(repos, sOpt, opts...)

	debug := llb.Scratch().File(llb.Mkfile("debug", 0o644, []byte(`debug=2`)), opts...)
	opts = append(opts, dalec.ProgressGroup("Install spec package"))

	// If we have base packages to install, create a meta-package to install them.
	if len(c.BasePackages) > 0 {
		runtimePkgs := make(dalec.PackageDependencyList, len(c.BasePackages))
		for _, pkgName := range c.BasePackages {
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

		basePkg := c.BuildPkg(ctx, client, sOpt, basePkgSpec, targetKey, opts...)

		// Update the base image to include the base packages.
		// This may include things that are necessary to even install the debSt package.
		// So this must be done separately from the debSt package.
		opts := append(opts, dalec.ProgressGroup("Install base image packages"))
		baseImg = baseImg.Run(
			dalec.WithConstraints(opts...),
			InstallLocalPkg(basePkg, true, opts...),
			dalec.WithMountedAptCache(c.AptCachePrefix, opts...),
		).Root()
	}

	worker := c.Worker(sOpt, dalec.Platform(sOpt.TargetPlatform), dalec.WithConstraints(opts...))

	return baseImg.Run(
		dalec.WithConstraints(opts...),
		withRepos,
		dalec.WithMountedAptCache(c.AptCachePrefix, opts...),
		// This file makes dpkg give more verbose output which can be useful when things go awry.
		llb.AddMount("/etc/dpkg/dpkg.cfg.d/99-dalec-debug", debug, llb.SourcePath("debug"), llb.Readonly),
		dalec.RunOptFunc(func(cfg *llb.ExecInfo) {
			// Warning: HACK here
			// The base ubuntu image has this `excludes` config file which prevents
			// installation of a lot of things, including doc files.
			// This is mounting over that file with an empty file so that our test suite
			// passes (as it is looking at these files).
			if !spec.GetArtifacts(targetKey).HasDocs() {
				return
			}

			tmp := llb.Scratch().File(llb.Mkfile("tmp", 0o644, nil), opts...)
			llb.AddMount("/etc/dpkg/dpkg.cfg.d/excludes", tmp, llb.SourcePath("tmp")).SetRunOption(cfg)
		}),
		InstallLocalPkg(debSt, true, opts...),
		frontend.IgnoreCache(client, targets.IgnoreCacheKeyContainer),
	).Root().
		With(dalec.InstallPostSymlinks(spec.GetImagePost(targetKey), worker, opts...))
}
