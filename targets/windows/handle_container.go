package windows

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"runtime"
	"sort"
	"sync"

	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
)

const (
	winltsc2019 = "mcr.microsoft.com/windows/nanoserver:ltsc2019"
	winltsc2022 = "mcr.microsoft.com/windows/nanoserver:ltsc2022"
	winltsc2025 = "mcr.microsoft.com/windows/nanoserver:ltsc2025"

	windowsSystemDir = "/Windows/System32/"
)

var (
	defaultbaseRefs = []string{
		winltsc2019,
		winltsc2022,
		winltsc2025,
	}
)

var (
	defaultPlatform = ocispecs.Platform{
		OS: outputKey,
		// NOTE: Windows is (currently) only supported on amd64.
		// Making this use runtime.GOARCH so that builds are more explicitly and not surprising.
		// If/when Windows is supported on another platform (ie arm64) this will work as expected.
		// Until then, if someone really wants to build an amd64 image from arm64 they'll need to set the platform explicitly in the build request.
		Architecture: runtime.GOARCH,
	}
)

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
		for _, ref := range defaultbaseRefs {
			bases = append(bases, dalec.BaseImage{
				Rootfs: dalec.Source{
					DockerImage: &dalec.SourceDockerImage{Ref: ref},
				},
			})
		}
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
				Platform: &basePlatform,
				ImageOpt: &sourceresolver.ResolveImageOpt{
					ResolveMode: dc.ImageResolveMode.String(),
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

	rb, err := dc.Build(ctx, func(ctx context.Context, platform *ocispecs.Platform, idx int) (ref gwclient.Reference, retCfg, retBaseCfg *dalec.DockerImageSpec, retErr error) {
		spec, err := frontend.LoadSpec(ctx, dc, platform)
		if err != nil {
			return nil, nil, nil, err
		}

		if err := validateRuntimeDeps(spec, targetKey); err != nil {
			return nil, nil, nil, fmt.Errorf("error validating windows spec: %w", err)
		}

		pg := dalec.ProgressGroup("Build windows container: " + spec.Name)
		worker, err := distroConfig.Worker(sOpt, pg)
		if err != nil {
			return nil, nil, nil, err
		}

		bin, err := buildBinaries(ctx, spec, worker, client, sOpt, targetKey)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("unable to build binary %w", err)
		}

		bi := bases[idx]

		if platform == nil {
			platform = &defaultPlatform
		}
		baseImage := bi.ToState(sOpt, pg, llb.Platform(*platform))
		out := baseImage.
			File(llb.Copy(bin, "/", windowsSystemDir)).
			With(copySymlinks(spec.GetImagePost(targetKey)))

		def, err := out.Marshal(ctx)
		if err != nil {
			return nil, nil, nil, err
		}

		res, err := client.Solve(ctx, gwclient.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, nil, nil, err
		}

		dt := cfgs[idx]

		var baseCfg dalec.DockerImageSpec
		if err := json.Unmarshal(cfgs[idx], &baseCfg); err != nil {
			return nil, nil, nil, errors.Wrap(err, "error unmarshalling base image config")
		}

		var img dalec.DockerImageSpec
		if err := json.Unmarshal(dt, &img); err != nil {
			return nil, nil, nil, errors.Wrap(err, "error unmarshalling base image config")
		}

		if err := dalec.BuildImageConfig(spec, targetKey, &img); err != nil {
			return nil, nil, nil, errors.Wrap(err, "error creating image config")
		}

		ref, err = res.SingleRef()
		return ref, &img, &baseCfg, err
	})
	if err != nil {
		return nil, err
	}

	return rb.Finalize()
}

func copySymlinks(post *dalec.PostInstall) llb.StateOption {
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
				s = s.File(llb.Mkdir(path.Dir(newpath), 0755, llb.WithParents(true)))
				s = s.File(llb.Copy(s, oldpath, newpath))
			}
		}

		return s
	}
}
