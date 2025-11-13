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

func (cfg *Config) BuildContainer(ctx context.Context, client gwclient.Client, worker llb.State, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, rpmDir llb.State, opts ...llb.ConstraintsOpt) llb.State {
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
	importRepos := []DnfInstallOpt{DnfWithMounts(repoMounts), DnfImportKeys(keyPaths)}

	rpmMountDir := "/tmp/rpms"

	installOpts := []DnfInstallOpt{DnfAtRoot(workPath)}
	installOpts = append(installOpts, importRepos...)
	installOpts = append(installOpts, []DnfInstallOpt{
		IncludeDocs(spec.GetArtifacts(targetKey).HasDocs()),
		dnfInstallWithConstraints(opts),
	}...)

	baseMountPath := rpmMountDir + "-base"
	basePkgs := llb.Scratch().File(llb.Mkdir("/RPMS", 0o755))
	pkgs := []string{
		filepath.Join(rpmMountDir, "**/*.rpm"),
	}

	if !skipBase && len(cfg.BasePackages) > 0 {
		opts := append(opts, dalec.ProgressGroup("Create base virtual package"))

		var basePkgStates []llb.State
		for _, spec := range cfg.BasePackages {
			pkg := cfg.BuildPkg(ctx, client, worker, sOpt, &spec, targetKey, opts...)
			basePkgStates = append(basePkgStates, pkg)
		}

		basePkgs = dalec.MergeAtPath(basePkgs, basePkgStates, "/")
		pkgs = append(pkgs, filepath.Join(baseMountPath, "**/*.rpm"))
	}

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
		worker := cfg.Worker(sOpt, pg, pc)

		deps := dalec.SortMapKeys(rtDeps)

		withDownloads := worker.Run(dalec.ShArgs("set -ex; mkdir -p /tmp/rpms/RPMS/$(uname -m)")).
			Run(cfg.Install(deps,
				DnfDownloadAllDeps("/tmp/rpms/RPMS/$(uname -m)"))).Root()
		rpmDir := llb.Scratch().File(llb.Copy(withDownloads, "/tmp/rpms", "/", dalec.WithDirContentsOnly()), pg)

		ctr := cfg.BuildContainer(ctx, client, worker, sOpt, spec, targetKey, rpmDir, pg, pc)

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
