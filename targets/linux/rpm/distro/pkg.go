package distro

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
	"github.com/project-dalec/dalec/packaging/linux/rpm"
	"github.com/project-dalec/dalec/targets"
)

var (
	defaultRepoConfig = &dnfRepoPlatform
)

func (c *Config) Validate(spec *dalec.Spec) error {
	if err := rpm.ValidateSpec(spec); err != nil {
		return err
	}

	return nil
}

func addGoCache(info *rpm.CacheInfo) {
	info.Caches = append(info.Caches, dalec.CacheConfig{
		GoBuild: &dalec.GoBuildCache{},
	})
}

func needsAutoGocache(spec *dalec.Spec, targetKey string) bool {
	for _, c := range spec.Build.Caches {
		if c.GoBuild != nil {
			return false
		}
	}

	if !spec.HasGomods() && !dalec.HasGolang(spec, targetKey) {
		return false
	}

	return true
}

func (c *Config) BuildPkg(ctx context.Context, client gwclient.Client, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, opts ...llb.ConstraintsOpt) llb.State {
	opts = append(opts, frontend.IgnoreCache(client))

	worker := c.Worker(sOpt, dalec.Platform(sOpt.TargetPlatform), dalec.WithConstraints(opts...))
	worker = worker.With(c.InstallBuildDeps(spec, sOpt, targetKey, opts...))

	// Preprocess after build deps are installed so generators can use build tools.
	if err := spec.Preprocess(sOpt, worker, opts...); err != nil {
		return dalec.ErrorState(worker, err)
	}

	br := rpm.BuildRootWithMacros(worker, spec, sOpt, targetKey, c.RPMMacros, opts...)

	specPath := filepath.Join("SPECS", spec.Name, spec.Name+".spec")

	builder := worker.With(dalec.SetBuildNetworkMode(spec))
	cacheInfo := rpm.CacheInfo{CacheIdentity: c.BuildCacheIdentity(), Caches: spec.Build.Caches}

	if needsAutoGocache(spec, targetKey) {
		addGoCache(&cacheInfo)
	}
	if len(cacheInfo.Caches) > 0 && cacheInfo.CacheIdentity == "" {
		return dalec.ErrorState(builder, fmt.Errorf("rpm distro cache identity is not set"))
	}

	buildOpts := append(opts, spec.Build.Steps.GetSourceLocation(builder), frontend.IgnoreCache(client, targets.IgnoreCacheKeyPkg))
	st := rpm.Build(br, builder, specPath, cacheInfo, buildOpts...)

	signed := frontend.MaybeSign(ctx, client, st, spec, targetKey, sOpt, opts...)

	// Merge the signed state with the original state
	// The signed files should overwrite the unsigned ones.
	st = st.File(llb.Copy(signed, "/", "/"), opts...)
	return st
}

// BuildSRPM builds only the source rpm (`src.rpm`) for the given spec.
//
// The source rpm is produced from the same buildroot that [Config.BuildPkg]
// uses, so the resulting source package is identical to the one a full rpm
// build would produce, except the package's %build/%install sections are never
// executed.
// The returned state contains the source rpm under `SRPMS`.
func (c *Config) BuildSRPM(ctx context.Context, client gwclient.Client, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, opts ...llb.ConstraintsOpt) llb.State {
	opts = append(opts, frontend.IgnoreCache(client))

	worker := c.Worker(sOpt, dalec.Platform(sOpt.TargetPlatform), dalec.WithConstraints(opts...))
	worker = worker.With(c.InstallBuildDeps(spec, sOpt, targetKey, opts...))

	// Preprocess after build deps are installed so generators can use build tools.
	if err := spec.Preprocess(sOpt, worker, opts...); err != nil {
		return dalec.ErrorState(worker, err)
	}

	br := rpm.BuildRootWithMacros(worker, spec, sOpt, targetKey, c.RPMMacros, opts...)

	specPath := filepath.Join("SPECS", spec.Name, spec.Name+".spec")

	builder := worker.With(dalec.SetBuildNetworkMode(spec))

	buildOpts := append(opts, frontend.IgnoreCache(client, targets.IgnoreCacheKeySrcPkg))
	st := rpm.BuildSourceRPM(br, builder, specPath, buildOpts...)

	signed := frontend.MaybeSign(ctx, client, st, spec, targetKey, sOpt, opts...)

	// Merge the signed state with the original state
	// The signed files should overwrite the unsigned ones.
	st = st.File(llb.Copy(signed, "/", "/"), opts...)
	return st
}

// HandleSourcePkg is the handler for the `<distro>/srpm` target which produces
// only the source rpm for the spec.
func (c *Config) HandleSourcePkg(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		if err := c.Validate(spec); err != nil {
			return nil, nil, fmt.Errorf("rpm: invalid spec: %w", err)
		}

		sOpt, err := frontend.SourceOptFromClient(ctx, client, platform)
		if err != nil {
			return nil, nil, err
		}

		pg := dalec.ProgressGroup("Building " + targetKey + " source rpm: " + spec.Name)
		pc := dalec.Platform(platform)
		opts := []llb.ConstraintsOpt{pg, pc}

		st := c.BuildSRPM(ctx, client, sOpt, spec, targetKey, opts...)

		def, err := st.Marshal(ctx, opts...)
		if err != nil {
			return nil, nil, fmt.Errorf("error marshalling llb: %w", err)
		}

		res, err := client.Solve(ctx, gwclient.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, nil, err
		}

		ref, err := res.SingleRef()
		if err != nil {
			return nil, nil, err
		}

		if platform == nil {
			p := platforms.DefaultSpec()
			platform = &p
		}
		return ref, &dalec.DockerImageSpec{Image: ocispecs.Image{Platform: *platform}}, nil
	})
}

// runTests runs the package tests
// The returned reference is the solved container state
func (cfg *Config) RunTests(ctx context.Context, client gwclient.Client, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		withTestDeps := cfg.InstallTestDeps(sOpt, targetKey, spec, opts...)
		runTests := frontend.RunTests(ctx, client, sOpt, spec, withTestDeps, targetKey, opts...)
		return in.With(runTests)
	}
}

func (cfg *Config) RepoMounts(repos []dalec.PackageRepositoryConfig, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) (llb.RunOption, []string) {
	opts = append(opts, dalec.ProgressGroup("Prepare custom repos"))
	repoConfig := cfg.RepoPlatformConfig
	if repoConfig == nil {
		repoConfig = defaultRepoConfig
	}

	withRepos := dalec.WithRepoConfigs(repos, repoConfig, sOpt, opts...)
	withData := dalec.WithRepoData(repos, sOpt, opts...)
	keyMounts, keyPaths := dalec.GetRepoKeys(repos, repoConfig, sOpt, opts...)

	return dalec.WithRunOptions(withRepos, withData, keyMounts), keyPaths
}

func (cfg *Config) InstallTestDeps(sOpt dalec.SourceOpts, targetKey string, spec *dalec.Spec, opts ...llb.ConstraintsOpt) llb.StateOption {
	deps := spec.GetPackageDeps(targetKey).GetTest()
	if len(deps) == 0 {
		return dalec.NoopStateOption
	}

	opts = append(opts, dalec.ProgressGroup("Install test dependencies"))

	return func(in llb.State) llb.State {
		repos := spec.GetTestRepos(targetKey)
		repoMounts, keyPaths := cfg.RepoMounts(repos, sOpt, opts...)
		importRepos := []DnfInstallOpt{
			DnfAtRoot("/tmp/rootfs"),
			DnfWithMounts(repoMounts),
			DnfImportKeys(keyPaths),
			DnfWithSourceOpts(sOpt),
			DnfInstallWithConstraints(opts),
		}

		worker := cfg.Worker(sOpt, dalec.Platform(sOpt.TargetPlatform), dalec.WithConstraints(opts...))
		return worker.Run(
			dalec.WithConstraints(opts...),
			cfg.Install(dalec.SortMapKeys(deps), importRepos...),
			deps.GetSourceLocation(in),
		).AddMount("/tmp/rootfs", in)
	}
}

func (cfg *Config) ExtractPkg(ctx context.Context, client gwclient.Client, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, rpmDir llb.State, opts ...llb.ConstraintsOpt) llb.State {
	deps := spec.GetPackageDeps(targetKey)
	depRpms := cfg.DownloadDeps(sOpt, spec, targetKey, deps.GetSysext(), opts...)

	opts = append(opts, dalec.ProgressGroup("Extracting RPMs"))
	worker := cfg.Worker(sOpt, dalec.Platform(sOpt.TargetPlatform), dalec.WithConstraints(opts...))

	return worker.Run(
		llb.Args([]string{"find", "/input", "-name", "*.rpm", "-exec", "sh", "-c", "rpm2cpio \"$1\" | cpio -idmv -D /output", "-", "{}", ";"}),
		llb.AddMount("/input/build", rpmDir, llb.SourcePath("/RPMS")),
		llb.AddMount("/input/deps", depRpms),
		dalec.WithConstraints(opts...),
	).AddMount("/output", llb.Scratch())
}
