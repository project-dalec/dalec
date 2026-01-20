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

// The implementation here is identical to that for the deb distro.
// TODO: can this be refactored to share code?
func (cfg *Config) HandleWorker(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		sOpt, err := frontend.SourceOptFromClient(ctx, client, platform)
		if err != nil {
			return nil, nil, err
		}

		pc := dalec.Platform(platform)
		buildPlat := nativeExecutorPlatform(client)
		st := cfg.workerWithBuildPlatform(sOpt, buildPlat, pc, frontend.IgnoreCache(client), dalec.ProgressGroup("Handle worker"))

		def, err := st.Marshal(ctx, pc)
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

// nativeExecutorPlatform returns the BuildKit worker's native (executor) platform.
// In most cases this matches platforms.DefaultSpec(), but using the worker-advertised
// platform avoids assuming the frontend container's platform is the executor's.
func nativeExecutorPlatform(client gwclient.Client) ocispecs.Platform {
	bo := client.BuildOpts()
	for _, w := range bo.Workers {
		if len(w.Platforms) > 0 {
			return platforms.Normalize(w.Platforms[0])
		}
	}
	return platforms.Normalize(platforms.DefaultSpec())
}

func samePlatform(a, b ocispecs.Platform) bool {
	na := platforms.Normalize(a)
	nb := platforms.Normalize(b)
	return platforms.Only(na).Match(nb) && platforms.Only(nb).Match(na)
}

func rpmArchFromPlatform(p ocispecs.Platform) (string, error) {
	switch p.Architecture {
	case "amd64":
		return "x86_64", nil
	case "386":
		return "i686", nil
	case "arm64":
		return "aarch64", nil
	case "arm":
		switch p.Variant {
		case "v7", "":
			return "armv7hl", nil
		case "v6":
			return "armv6hl", nil
		default:
			return "", errors.Errorf("unsupported arm variant for rpm: %q", p.Variant)
		}
	default:
		return "", errors.Errorf("unsupported platform arch for rpm: %q", p.Architecture)
	}
}

func (cfg *Config) Worker(sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) llb.State {
	buildPlat := platforms.Normalize(platforms.DefaultSpec())
	return cfg.workerWithBuildPlatform(sOpt, buildPlat, opts...)
}

func (cfg *Config) workerWithBuildPlatform(sOpt dalec.SourceOpts, buildPlat ocispecs.Platform, opts ...llb.ConstraintsOpt) llb.State {

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

	targetPlat := buildPlat
	if sOpt.TargetPlatform != nil {
		targetPlat = platforms.Normalize(*sOpt.TargetPlatform)
	}

	targetSOpt := sOpt
	targetSOpt.TargetPlatform = &targetPlat
	buildSOpt := sOpt
	buildSOpt.TargetPlatform = &buildPlat

	targetOpts := append(append([]llb.ConstraintsOpt{}, opts...), llb.Platform(targetPlat))
	buildOpts := append(append([]llb.ConstraintsOpt{}, opts...), llb.Platform(buildPlat))

	targetBase := frontend.GetBaseImage(targetSOpt, cfg.ImageRef, targetOpts...).Platform(targetPlat)

	installOpts := []DnfInstallOpt{
		DnfInstallWithConstraints(opts),
	}

	if samePlatform(targetPlat, buildPlat) {
		return targetBase.Run(
			dalec.WithConstraints(append(opts, llb.Platform(targetPlat))...),
			cfg.Install(cfg.BuilderPackages, installOpts...),
		).Root()
	}

	targetArch, err := rpmArchFromPlatform(targetPlat)
	if err != nil {
		return dalec.ErrorState(llb.Scratch(), err)
	}

	buildBase := frontend.GetBaseImage(buildSOpt, cfg.ImageRef, buildOpts...).Platform(buildPlat)
	const rootfsMount = "/tmp/dalec/rootfs"

	// Cross-arch installs always use dnf --forcearch --installroot. Ensure dnf exists
	// on the build/executor platform deterministically before running InstallIntoRoot.
	buildBase = buildBase.Run(
		dalec.WithConstraints(append(opts, llb.Platform(buildPlat))...),
		cfg.Install([]string{"dnf"}, installOpts...),
	).Root()

	es := buildBase.Run(
		dalec.WithConstraints(append(opts, llb.Platform(buildPlat))...),
		llb.AddMount(rootfsMount, targetBase),
		cfg.InstallIntoRoot(rootfsMount, cfg.BuilderPackages, targetArch, buildPlat),
	)
	return es.GetMount(rootfsMount).Platform(targetPlat)

}

func (cfg *Config) SysextWorker(sOpts dalec.SourceOpts, opts ...llb.ConstraintsOpt) llb.State {
	buildPlat := platforms.Normalize(platforms.DefaultSpec())
	worker := cfg.workerWithBuildPlatform(sOpts, buildPlat, opts...)

	targetPlat := buildPlat
	if sOpts.TargetPlatform != nil {
		targetPlat = platforms.Normalize(*sOpts.TargetPlatform)
	}

	installOpts := []DnfInstallOpt{
		DnfInstallWithConstraints(opts),
	}

	if samePlatform(targetPlat, buildPlat) {
		return worker.Run(
			dalec.WithConstraints(append(opts, llb.Platform(targetPlat))...),
			cfg.Install([]string{"erofs-utils"}, installOpts...),
		).Root()
	}

	targetArch, err := rpmArchFromPlatform(targetPlat)
	if err != nil {
		return dalec.ErrorState(llb.Scratch(), err)
	}

	buildSOpt := sOpts
	buildSOpt.TargetPlatform = &buildPlat
	buildOpts := append(append([]llb.ConstraintsOpt{}, opts...), llb.Platform(buildPlat))

	const rootfsMount = "/tmp/dalec/rootfs"
	buildBase := frontend.GetBaseImage(buildSOpt, cfg.ImageRef, buildOpts...).Platform(buildPlat)

	buildBase = buildBase.Run(
		dalec.WithConstraints(append(opts, llb.Platform(buildPlat))...),
		cfg.Install([]string{"dnf"}, installOpts...),
	).Root()

	es := buildBase.Run(
		dalec.WithConstraints(append(opts, llb.Platform(buildPlat))...),
		llb.AddMount(rootfsMount, worker),
		cfg.InstallIntoRoot(rootfsMount, []string{"erofs-utils"}, targetArch, buildPlat),
	)

	return es.GetMount(rootfsMount).Platform(targetPlat)
}
