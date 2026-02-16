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

func extraRepos(input buildContainerInput) llb.RunOption {
	// Those base repos come from distro configuration.
	repos := dalec.GetExtraRepos(input.Config.ExtraRepos, "install")

	// These are user specified via spec.
	repos = append(repos, input.Spec.GetInstallRepos(input.Target)...)

	return input.Config.RepoMounts(repos, input.SOpt, input.Opts...)
}

func installPackagesInContainer(input buildContainerInput, ro []llb.RunOption) llb.StateOption {
	return func(baseImg llb.State) llb.State {
		opts := append(input.Opts, dalec.ProgressGroup("Install spec package"))

		debug := llb.Scratch().File(llb.Mkfile("debug", 0o644, []byte(`debug=2`)), opts...)

		return baseImg.Run(
			append(ro,
				dalec.WithConstraints(opts...),
				extraRepos(input),
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
