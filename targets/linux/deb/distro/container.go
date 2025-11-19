package distro

import (
	"context"
	"fmt"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
	"github.com/project-dalec/dalec/targets"
)

func (c *Config) BuildContainer(ctx context.Context, client gwclient.Client, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, debSt llb.State, opts ...llb.ConstraintsOpt) llb.State {
	bi, err := spec.GetSingleBase(targetKey)
	if err != nil {
		return dalec.ErrorState(llb.Scratch(), err)
	}

	opts = append(opts, frontend.IgnoreCache(client))

	var baseImg llb.State
	if bi != nil {
		img := bi.ToState(sOpt, opts...)
		baseImg = img
	} else {
		if c.DefaultOutputImage == "" {
			return dalec.ErrorState(llb.Scratch(), fmt.Errorf("no output image ref specified, cannot build from scratch"))
		}
		baseImg = llb.Image(c.DefaultOutputImage, llb.WithMetaResolver(sOpt.Resolver), dalec.WithConstraints(opts...))
	}

	opts = append(opts, dalec.ProgressGroup("Build Container Image"))

	repos := dalec.GetExtraRepos(c.ExtraRepos, "install")
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
			llb.AddEnv("DEBIAN_FRONTEND", "noninteractive"),
			dalec.WithMountedAptCache(c.AptCachePrefix),
			InstallLocalPkg(basePkg, true, opts...),
			dalec.WithMountedAptCache(c.AptCachePrefix),
		).Root()
	}

	worker := c.Worker(sOpt, dalec.Platform(sOpt.TargetPlatform), dalec.WithConstraints(opts...))

	return baseImg.Run(
		dalec.WithConstraints(opts...),
		withRepos,
		llb.AddEnv("DEBIAN_FRONTEND", "noninteractive"),
		dalec.WithMountedAptCache(c.AptCachePrefix),
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
