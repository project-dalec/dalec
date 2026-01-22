package distro

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
	"github.com/project-dalec/dalec/frontend/pkg/bkfs"
	"github.com/project-dalec/dalec/packaging/linux/deb"
	"github.com/project-dalec/dalec/targets"
)

func (d *Config) Validate(spec *dalec.Spec) error {
	return nil
}

func (d *Config) BuildPkg(ctx context.Context, client gwclient.Client, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, opts ...llb.ConstraintsOpt) llb.State {
	opts = append(opts, dalec.ProgressGroup("Build deb package"))

	worker := d.Worker(sOpt, dalec.Platform(sOpt.TargetPlatform), dalec.WithConstraints(opts...))

	versionID := d.VersionID
	if versionID == "" {
		var err error
		versionID, err = deb.ReadDistroVersionID(ctx, client, worker)
		if err != nil {
			return dalec.ErrorState(worker, err)
		}
	}

	worker = worker.With(d.InstallBuildDeps(ctx, sOpt, spec, targetKey, append(opts, frontend.IgnoreCache(client))...))

	// Preprocess the spec to generate patches for gomod edits and other generators
	// This must happen after build deps are installed so Go is available
	if err := spec.Preprocess(sOpt, worker, opts...); err != nil {
		return dalec.ErrorState(worker, err)
	}

	var cfg deb.SourcePkgConfig
	extraPaths := prepareGo(ctx, client, &cfg, worker, spec, targetKey, opts...)

	srcPkg := deb.SourcePackage(ctx, sOpt, worker.With(extraPaths), spec, targetKey, versionID, cfg, append(opts, frontend.IgnoreCache(client, targets.IgnoreCacheKeySrcPkg))...)

	builder := worker.With(dalec.SetBuildNetworkMode(spec))

	buildOpts := append(opts, spec.Build.Steps.GetSourceLocation(builder))
	st := deb.BuildDeb(builder, spec, srcPkg, versionID, append(buildOpts, frontend.IgnoreCache(client, targets.IgnoreCacheKeyPkg))...)

	// Filter out everything except the .deb files
	filtered := llb.Scratch().File(
		llb.Copy(st, "/", "/",
			dalec.WithIncludes([]string{"**/*.deb"}),
		),
		opts...,
	)

	signed := frontend.MaybeSign(ctx, client, filtered, spec, targetKey, sOpt, opts...)

	// Merge the signed state with the original state
	// The signed files should overwrite the unsigned ones.
	st = st.File(llb.Copy(signed, "/", "/"), opts...)
	return st
}

func noOpStateOpt(in llb.State) llb.State {
	return in
}

func prepareGo(ctx context.Context, client gwclient.Client, cfg *deb.SourcePkgConfig, worker llb.State, spec *dalec.Spec, targetKey string, opts ...llb.ConstraintsOpt) llb.StateOption {
	if !dalec.HasGolang(spec, targetKey) && !spec.HasGomods() {
		return noOpStateOpt
	}

	addGoCache := true
	for _, c := range spec.Build.Caches {
		if c.GoBuild != nil {
			addGoCache = false
		}
	}

	if addGoCache {
		spec.Build.Caches = append(spec.Build.Caches, dalec.CacheConfig{
			GoBuild: &dalec.GoBuildCache{},
		})
	}

	goBin, err := searchForAltGolang(ctx, client, spec, targetKey, worker, opts...)
	if err != nil {
		return dalec.ErrorStateOption(errors.Wrap(err, "error searching for alternate go binary"))
	}

	if goBin == "" {
		return noOpStateOpt
	}
	cfg.PrependPath = append(cfg.PrependPath, goBin)
	return addPaths([]string{goBin}, opts...)
}

func searchForAltGolang(ctx context.Context, client gwclient.Client, spec *dalec.Spec, targetKey string, in llb.State, opts ...llb.ConstraintsOpt) (string, error) {
	if !spec.HasGomods() {
		return "", nil
	}

	deps := spec.GetPackageDeps(targetKey).GetBuild()
	if _, hasNormalGo := deps["golang"]; hasNormalGo {
		return "", nil
	}

	candidates := buildCandidatePaths(deps, "golang", "usr/lib/go", "/bin")
	return findBinaryInCandidates(ctx, client, in, candidates, "go", opts...)
}

// buildCandidatePaths extracts version-specific package paths from dependencies
func buildCandidatePaths(deps map[string]dalec.PackageConstraints, prefix, basePath, suffix string) []string {
	var candidates []string

	for dep := range deps {
		if strings.HasPrefix(dep, prefix+"-") {
			// Get the base version component
			_, ver, _ := strings.Cut(dep, "-")
			// Trim off any potential extra stuff like `golang-1.20-go` (ie the `-go` bit)
			// This is just for having definitive search paths to check it should
			// not be an issue if this is not like the above example and its
			// something else like `-doc` since we are still going to check the
			// binary exists anyway (plus this would be highly unlikely in any case).
			ver, _, _ = strings.Cut(ver, "-")
			candidates = append(candidates, basePath+"-"+ver+suffix)
		}
	}

	return candidates
}

// findBinaryInCandidates searches for a binary in a list of candidate paths
func findBinaryInCandidates(ctx context.Context, client gwclient.Client, in llb.State, candidates []string, binaryName string, opts ...llb.ConstraintsOpt) (string, error) {
	if len(candidates) == 0 {
		return "", nil
	}

	stfs, err := bkfs.FromState(ctx, &in, client, opts...)
	if err != nil {
		return "", err
	}

	for _, p := range candidates {
		_, err := fs.Stat(stfs, filepath.Join(p, binaryName))
		if err == nil {
			// bkfs does not allow a leading `/` in the stat path per spec for [fs.FS]
			// Add that in here
			p := "/" + p
			return p, nil
		}
	}

	return "", nil
}

func addPaths(paths []string, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		if len(paths) == 0 {
			return in
		}
		return in.Async(func(ctx context.Context, in llb.State, c *llb.Constraints) (llb.State, error) {
			opts := []llb.ConstraintsOpt{dalec.WithConstraint(c), dalec.WithConstraints(opts...)}
			pathEnv, _, err := in.GetEnv(ctx, "PATH", opts...)
			if err != nil {
				return in, err
			}
			return in.AddEnv("PATH", strings.Join(append(paths, pathEnv), ":")), nil
		})
	}
}

func (cfg *Config) RunTests(ctx context.Context, client gwclient.Client, spec *dalec.Spec, sOpt dalec.SourceOpts, final llb.State, targetKey string, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		deps := cfg.InstallTestDeps(sOpt, targetKey, spec, opts...)
		tests := frontend.RunTests(ctx, client, spec, final, targetKey, sOpt.TargetPlatform)
		return in.With(deps).With(tests)
	}
}

func (cfg *Config) HandleSourcePkg(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		sOpt, err := frontend.SourceOptFromClient(ctx, client, platform)
		if err != nil {
			return nil, nil, err
		}

		pg := dalec.ProgressGroup(spec.Name)
		pc := dalec.Platform(platform)

		worker := cfg.Worker(sOpt, pg, pc, frontend.IgnoreCache(client, cfg.ImageRef, cfg.ContextRef))

		versionID, err := deb.ReadDistroVersionID(ctx, client, worker)
		if err != nil {
			return nil, nil, err
		}

		worker = worker.With(cfg.InstallBuildDeps(ctx, sOpt, spec, targetKey, pg, frontend.IgnoreCache(client)))

		var cfg deb.SourcePkgConfig
		extraPaths := prepareGo(ctx, client, &cfg, worker, spec, targetKey, pg, frontend.IgnoreCache(client))

		st := deb.SourcePackage(ctx, sOpt, worker.With(extraPaths), spec, targetKey, versionID, cfg, pg, pc, frontend.IgnoreCache(client, targets.IgnoreCacheKeySrcPkg))

		def, err := st.Marshal(ctx, frontend.IgnoreCache(client))
		if err != nil {
			return nil, nil, errors.Wrap(err, "error marshalling source package state")
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
		return ref, nil, nil
	})
}

func (c *Config) ExtractPkg(ctx context.Context, client gwclient.Client, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, debSt llb.State, opts ...llb.ConstraintsOpt) llb.State {
	worker := c.Worker(sOpt, dalec.Platform(sOpt.TargetPlatform), dalec.WithConstraints(opts...))
	depDebs := llb.Scratch()
	deps := spec.GetPackageDeps(targetKey).GetSysext()
	if len(deps) > 0 {
		opts = append(opts, deps.GetSourceLocation(worker))
		depDebs = c.DownloadDeps(worker, sOpt, spec, targetKey, deps, opts...)
	}

	opts = append(opts, dalec.ProgressGroup("Extracting DEBs"))

	return worker.Run(
		llb.Args([]string{"find", "/input", "-name", "*.deb", "-exec", "dpkg-deb", "--verbose", "--extract", "{}", "/output", ";"}),
		llb.AddMount("/input/build", debSt),
		llb.AddMount("/input/deps", depDebs),
		dalec.WithConstraints(opts...),
	).AddMount("/output", llb.Scratch())
}
