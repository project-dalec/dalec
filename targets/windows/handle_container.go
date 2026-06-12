package windows

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"runtime"
	"sort"
	"sync"

	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
	"golang.org/x/sync/errgroup"
)

const (
	defaultBaseImage = "mcr.microsoft.com/windows/nanoserver:1809"
	windowsSystemDir = "/Windows/System32/"
)

var defaultPlatform = ocispecs.Platform{
	OS: outputKey,
	// NOTE: Windows is (currently) only supported on amd64.
	// Making this use runtime.GOARCH so that builds are more explicitly and not surprising.
	// If/when Windows is supported on another platform (ie arm64) this will work as expected.
	// Until then, if someone really wants to build an amd64 image from arm64 they'll need to set the platform explicitly in the build request.
	Architecture: runtime.GOARCH,
}

func handleContainer(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	dc, err := dockerui.NewClient(client)
	if err != nil {
		return nil, err
	}

	if len(dc.TargetPlatforms) > 1 {
		return nil, fmt.Errorf("multi-platform output is not supported")
	}

	sOpt := frontend.SourceOptFromUIClient(ctx, client, dc, nil)

	spec, err := frontend.LoadSpec(ctx, dc, nil)
	if err != nil {
		return nil, err
	}

	targetKey := frontend.GetTargetKey(client)
	bases := spec.GetImageBases(targetKey)

	if len(bases) == 0 {
		bases = append(bases, dalec.BaseImage{
			Rootfs: dalec.Source{
				DockerImage: &dalec.SourceDockerImage{Ref: defaultBaseImage},
			},
		})
	}

	eg, grpCtx := errgroup.WithContext(ctx)
	var mu sync.Mutex
	cfgs := make([][]byte, len(bases))
	targets := make([]ocispecs.Platform, len(cfgs))

	basePlatform := defaultPlatform
	if len(dc.TargetPlatforms) > 0 {
		basePlatform = dc.TargetPlatforms[0]
	}

	for idx, bi := range bases {
		idx := idx
		bi := bi
		eg.Go(func() error {
			dt, err := bi.ResolveImageConfig(grpCtx, sOpt, sourceresolver.Opt{
				ImageOpt: &sourceresolver.ResolveImageOpt{
					ResolveMode: dc.ImageResolveMode.String(),
					Platform:    &basePlatform,
				},
			})
			if err != nil {
				return err
			}

			var cfg dalec.DockerImageSpec
			if err := json.Unmarshal(dt, &cfg); err != nil {
				return errors.Wrapf(err, "error unmarshalling base image config for base image at index %d", idx)
			}

			mu.Lock()
			cfgs[idx] = dt
			targets[idx] = cfg.Platform
			mu.Unlock()

			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	seen := make(map[string]struct{})
	for _, p := range targets {
		s := platforms.FormatAll(p)
		if _, ok := seen[s]; ok {
			return nil, fmt.Errorf("multiple base images provided with the same platform value")
		}
		seen[s] = struct{}{}
	}

	dc.TargetPlatforms = targets
	if len(targets) > 1 {
		dc.MultiPlatformRequested = true
	}

	rb, err := dc.Build(ctx, func(ctx context.Context, platform *ocispecs.Platform, idx int) (*dockerui.BuildResult, error) {
		spec, err := frontend.LoadSpec(ctx, dc, platform)
		if err != nil {
			return nil, err
		}

		if err := validateRuntimeDeps(spec, targetKey); err != nil {
			return nil, fmt.Errorf("error validating windows spec: %w", err)
		}

		pg := dalec.ProgressGroup("Build windows container: " + spec.Name)
		worker := distroConfig.Worker(sOpt, pg)

		bin := buildBinaries(ctx, spec, worker, client, sOpt, targetKey, pg)

		bi := bases[idx]

		if platform == nil {
			platform = &defaultPlatform
		}
		baseImage := bi.ToState(sOpt, pg, llb.Platform(*platform))

		// Install every package's binaries (primary + supplemental) into the
		// image, matching the linux container target which installs all packages
		// produced for the target.
		//
		// The primary package's binaries are at the root of bin, alongside the
		// supplemental package subdirs. Always copy the root contents (excluding
		// those subdirs) so the build step is realized in the graph even when the
		// primary package has no binaries. Otherwise buildkit would prune the
		// build, and specs that rely on a failing build step would wrongly succeed.
		pkgs := windowsPackages(spec, targetKey)

		var subPackageDirs []string
		for _, pkg := range pkgs {
			if !pkg.Primary {
				subPackageDirs = append(subPackageDirs, pkg.Name)
			}
		}

		out := baseImage.File(
			llb.Copy(bin, "/", windowsSystemDir, dalec.WithDirContentsOnly(), llb.WithExcludePatterns(subPackageDirs)),
			pg,
		)

		// Flatten each supplemental package's binaries into the system directory.
		for _, pkg := range pkgs {
			if pkg.Primary || len(pkg.Binaries) == 0 {
				continue
			}
			out = out.File(llb.Copy(bin, "/"+pkg.Name+"/", windowsSystemDir, dalec.WithDirContentsOnly()), pg)
		}
		out = out.With(copySymlinks(spec.GetImagePost(targetKey), pg))

		def, err := out.Marshal(ctx)
		if err != nil {
			return nil, err
		}

		res, err := client.Solve(ctx, gwclient.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, err
		}

		dt := cfgs[idx]

		var baseCfg dalec.DockerImageSpec
		if err := json.Unmarshal(cfgs[idx], &baseCfg); err != nil {
			return nil, errors.Wrap(err, "error unmarshalling base image config")
		}

		var img dalec.DockerImageSpec
		if err := json.Unmarshal(dt, &img); err != nil {
			return nil, errors.Wrap(err, "error unmarshalling base image config")
		}

		if err := dalec.BuildImageConfig(spec, targetKey, &img); err != nil {
			return nil, errors.Wrap(err, "error creating image config")
		}

		ref, err := res.SingleRef()
		if err != nil {
			return nil, err
		}

		return &dockerui.BuildResult{
			Reference: ref,
			Image:     &img,
			BaseImage: &baseCfg,
		}, nil
	})
	if err != nil {
		return nil, err
	}

	return rb.Finalize()
}

func copySymlinks(post *dalec.PostInstall, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(s llb.State) llb.State {
		if post == nil {
			return s
		}

		if len(post.Symlinks) == 0 {
			return s
		}

		sortedKeys := dalec.SortMapKeys(post.Symlinks)
		for _, oldpath := range sortedKeys {
			newpaths := post.Symlinks[oldpath].Paths
			sort.Strings(newpaths)

			for _, newpath := range newpaths {
				s = s.File(llb.Mkdir(path.Dir(newpath), 0755, llb.WithParents(true)), opts...)
				s = s.File(llb.Copy(s, oldpath, newpath), opts...)
			}
		}

		return s
	}
}
