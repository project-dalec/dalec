package distro

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
	"github.com/project-dalec/dalec/targets"
	"github.com/project-dalec/dalec/targets/linux"
)

func (cfg *Config) BuildContainer(ctx context.Context, client gwclient.Client, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, rpmDir llb.State, opts ...llb.ConstraintsOpt) llb.State {
	opts = append(opts, dalec.ProgressGroup("Install RPMs"))
	opts = append(opts, frontend.IgnoreCache(client))

	const workPath = "/tmp/rootfs"

	bi, err := spec.GetSingleBase(targetKey)
	if err != nil {
		return dalec.ErrorState(llb.Scratch(), err)
	}

	skipBase := bi != nil
	rootfs := bi.ToState(sOpt, opts...)

	installTimeRepos := spec.GetInstallRepos(targetKey)
	repoMounts, keyPaths := cfg.RepoMounts(installTimeRepos, sOpt, opts...)
	importRepos := []DnfInstallOpt{
		DnfWithMounts(repoMounts),
		DnfImportKeys(keyPaths),
		DnfInstallWithConstraints(opts),
	}

	rpmMountDir := "/tmp/rpms"

	installOpts := []DnfInstallOpt{DnfAtRoot(workPath)}
	installOpts = append(installOpts, importRepos...)
	installOpts = append(installOpts, []DnfInstallOpt{
		IncludeDocs(spec.GetArtifacts(targetKey).HasDocs()),
	}...)

	baseMountPath := rpmMountDir + "-base"
	basePkgs := llb.Scratch().File(llb.Mkdir("/RPMS", 0o755), opts...)
	pkgs := []string{
		filepath.Join(rpmMountDir, "**/*.rpm"),
	}

	if !skipBase && len(cfg.BasePackages) > 0 {
		opts := append(opts, dalec.ProgressGroup("Create base virtual package"))

		var basePkgStates []llb.State
		for _, spec := range cfg.BasePackages {
			pkg := cfg.BuildPkg(ctx, client, sOpt, &spec, targetKey, opts...)
			basePkgStates = append(basePkgStates, pkg)
		}

		basePkgs = dalec.MergeAtPath(basePkgs, basePkgStates, "/", opts...)
		pkgs = append(pkgs, filepath.Join(baseMountPath, "**/*.rpm"))
	}

	worker := cfg.Worker(sOpt, dalec.Platform(sOpt.TargetPlatform), dalec.WithConstraints(opts...))

	rootfs = worker.Run(
		dalec.WithConstraints(opts...), // Make sure constraints (and platform specifically) are applied before install is set
		cfg.Install(pkgs, installOpts...),
		llb.AddMount(rpmMountDir, rpmDir, llb.SourcePath("/RPMS")),
		llb.AddMount(baseMountPath, basePkgs, llb.SourcePath("/RPMS")),
		frontend.IgnoreCache(client, targets.IgnoreCacheKeyContainer),
	).AddMount(workPath, rootfs)

	if post := spec.GetImagePost(targetKey); post != nil && len(post.Symlinks) > 0 {
		rootfs = rootfs.With(dalec.InstallPostSymlinks(post, worker, opts...))
	}

	return rootfs
}

func (cfg *Config) HandleDepsOnly(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		rtDeps := spec.GetPackageDeps(targetKey).GetRuntime()
		if len(rtDeps) == 0 {
			return nil, nil, fmt.Errorf("no runtime deps found for '%s'", targetKey)
		}

		pg := dalec.ProgressGroup("Build " + targetKey + " deps-only container for: " + spec.Name)

		sOpt, err := frontend.SourceOptFromClient(ctx, client, platform)
		if err != nil {
			return nil, nil, err
		}

		pc := dalec.Platform(platform)

		// NOTE: Deps-only allows bare specs, ie specs with just the runtime deps included.
		// This means we may need to fill in some of the details that are required by the package manager.
		depsSpec := &dalec.Spec{
			Name:        spec.Name + "-runtime-deps",
			License:     spec.License,
			Version:     spec.Version,
			Revision:    spec.Revision,
			Description: "Runtime dependencies meta package",
			Dependencies: &dalec.PackageDependencies{
				Runtime: rtDeps,
			},
		}

		if depsSpec.Name == "-runtime-deps" {
			// Name cannot start with "-"
			depsSpec.Name = "dalec-user" + depsSpec.Name
		}
		if depsSpec.Version == "" {
			depsSpec.Version = "0.0.1"
		}
		if depsSpec.Revision == "" {
			depsSpec.Revision = "1"
		}
		if depsSpec.License == "" {
			depsSpec.License = "MIT"
		}

		pkg := cfg.BuildPkg(ctx, client, sOpt, depsSpec, targetKey, pg)
		ctr := cfg.BuildContainer(ctx, client, sOpt, spec, targetKey, pkg, pg)

		def, err := ctr.Marshal(ctx, pc)
		if err != nil {
			return nil, nil, err
		}

		res, err := client.Solve(ctx, gwclient.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, nil, err
		}

		img, err := linux.BuildImageConfig(ctx, sOpt, spec, platform, targetKey)
		if err != nil {
			return nil, nil, err
		}

		ref, err := res.SingleRef()
		if err != nil {
			return nil, nil, err
		}

		return ref, img, nil
	})
}
