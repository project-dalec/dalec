package distro

import (
	"context"
	"encoding/json"

	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
)

func (cfg *Config) HandleWorker(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		sOpt, err := frontend.SourceOptFromClient(ctx, client, platform)
		if err != nil {
			return nil, nil, err
		}

		p := platforms.DefaultSpec()
		if platform != nil {
			p = *platform
		}
		pc := llb.Platform(p)

		ignoreCache := frontend.IgnoreCache(client, cfg.ImageRef, cfg.ContextRef)
		st := cfg.Worker(sOpt, pc, ignoreCache)

		def, err := st.Marshal(ctx, pc, ignoreCache)
		if err != nil {
			return nil, nil, err
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

		_, _, dt, err := client.ResolveImageConfig(ctx, cfg.ImageRef, sourceresolver.Opt{
			ImageOpt: &sourceresolver.ResolveImageOpt{
				Platform: platform,
			},
		})
		if err != nil {
			return nil, nil, err
		}

		var cfg dalec.DockerImageSpec
		if err := json.Unmarshal(dt, &cfg); err != nil {
			return nil, nil, err
		}
		return ref, &cfg, nil
	})
}

func (cfg *Config) Worker(sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) llb.State {
	opts = append(opts, dalec.ProgressGroup("Prepare worker image"))
	if cfg.ContextRef != "" {
		st, err := sOpt.GetContext(cfg.ContextRef, dalec.WithConstraints(opts...))
		if err != nil {
			return dalec.ErrorState(llb.Scratch(), errors.Wrap(err, "error getting worker context"))
		}
		if st != nil {
			return *st
		}
	}

	return frontend.GetBaseImage(sOpt, cfg.ImageRef, opts...).
		Run(
			dalec.WithConstraints(opts...),
			AptInstall(cfg.BuilderPackages, opts...),
			dalec.WithMountedAptCache(cfg.AptCachePrefix, opts...),
		).Root()
}

func (cfg *Config) SysextWorker(sOpts dalec.SourceOpts, opts ...llb.ConstraintsOpt) llb.State {
	worker := cfg.Worker(sOpts, opts...)
	return worker.Run(
		dalec.WithConstraints(opts...),
		AptInstall([]string{"erofs-utils"}, opts...),
		dalec.WithMountedAptCache(cfg.AptCachePrefix, opts...),
	).Root()
}
