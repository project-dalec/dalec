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

	debug := llb.Scratch().File(llb.Mkfile("debug", 0o644, []byte(`debug=2`)), opts...)
	// This file makes dpkg give more verbose output which can be useful when things go awry.
	debugOpt := llb.AddMount("/etc/dpkg/dpkg.cfg.d/99-dalec-debug", debug, llb.SourcePath("debug"), llb.Readonly)

	// Distroless images are built from scratch.
	baseImg := llb.Scratch().
		File(llb.Mkfile("/apt.conf", 0o644, []byte(`#Apt::Architecture "amd64";
#Apt::Architectures "amd64";
Dir "/tmp/rootfs";
Dir::Etc::TrustedParts "/etc/apt/trusted.gpg.d/";
Dir::Etc::sourcelist "/etc/apt/sources.list";
Dir::Etc::sourceparts "/etc/apt/sources.list.d/";
`)), opts...).
		File(llb.Mkdir("/etc", 0o755), opts...).
		File(llb.Mkdir("/etc/apt", 0o755), opts...).
		File(llb.Mkdir("/etc/apt/apt.conf.d", 0o755), opts...).
		File(llb.Mkdir("/etc/apt/preferences.d", 0o755), opts...).
		File(llb.Mkdir("/var", 0o755), opts...).
		File(llb.Mkdir("/var/cache", 0o755), opts...).
		File(llb.Mkdir("/var/lib", 0o755), opts...).
		File(llb.Mkdir("/var/lib/dpkg", 0o755), opts...).
		File(llb.Mkfile("/var/lib/dpkg/status", 0o755, []byte{}), opts...)

	baseImg = input.Worker.Run(
		dalec.WithConstraints(opts...),
		withRepos,
		debugOpt,
		llb.AddEnv("DEBIAN_FRONTEND", "noninteractive"),
		llb.AddEnv("APT_CONFIG", "/tmp/rootfs/apt.conf"),
		dalec.ShArgs("set -x; cp -r /etc/apt/sources.list* /tmp/rootfs/etc/apt/ && apt-get update && apt-get --yes --download-only install $(dpkg-query -Wf '${Package} ${Essential}\n' | awk '$2 == \"yes\" {print $1}') && for f in /tmp/rootfs/var/cache/apt/archives/*.deb; do dpkg-deb --extract \"$f\" /tmp/rootfs; done"),
		frontend.IgnoreCache(input.Client, targets.IgnoreCacheKeyContainer),
	).AddMount("/tmp/rootfs", baseImg).Run(
		dalec.WithConstraints(opts...),
		debugOpt,
		llb.AddEnv("DEBIAN_FRONTEND", "noninteractive"),
		dalec.ShArgs("dpkg --install --force-depends /var/cache/apt/archives/*.deb && rm -rf /var/cache/apt/archives/*.deb"),
		frontend.IgnoreCache(input.Client, targets.IgnoreCacheKeyContainer),
	).Root()

	bi, err := input.Spec.GetSingleBase(input.Target)
	if err != nil {
		return dalec.ErrorState(llb.Scratch(), err)
	}
	if bi != nil {
		baseImg = bi.ToState(input.SOpt, opts...)
	}

	opts = append(opts, dalec.ProgressGroup("Install spec package"))

	// If we have base packages to install, create a meta-package to install them.
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

		basePkg := input.Config.BuildPkg(ctx, input.Client, input.SOpt, basePkgSpec, input.Target, opts...)

		// Update the base image to include the base packages.
		// This may include things that are necessary to even install the debSt package.
		// So this must be done separately from the debSt package.
		opts := append(opts, dalec.ProgressGroup("Install base image packages"))
		baseImg = baseImg.Run(
			dalec.WithConstraints(opts...),
			debugOpt,
			withRepos,
			InstallLocalPkg(basePkg, true, opts...),
			dalec.WithMountedAptCache(input.Config.AptCachePrefix, opts...),
		).Root()
	}

	return baseImg.Run(
		dalec.WithConstraints(opts...),
		withRepos,
		debugOpt,
		InstallLocalPkg(input.DebSt, true, opts...),
		frontend.IgnoreCache(input.Client, targets.IgnoreCacheKeyContainer),
	).Root().
		With(dalec.InstallPostSymlinks(input.Spec.GetImagePost(input.Target), input.Worker, opts...))
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
