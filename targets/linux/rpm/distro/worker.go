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
		var normPlatform *ocispecs.Platform
                if platform != nil {
                        p := platforms.Normalize(*platform)
                        normPlatform = &p
                }

		sOpt, err := frontend.SourceOptFromClient(ctx, client, normPlatform)
		if err != nil {
			return nil, nil, err
		}

		pc := dalec.Platform(normPlatform)
                buildPlat := nativeExecutorPlatform(client)
                st := cfg.workerWithBuildPlatform(sOpt, buildPlat, pc, frontend.IgnoreCache(client))

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
			Platform: normPlatform,
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
// Do NOT use platforms.DefaultSpec() for this: the frontend image may be running as the
// TARGET platform (multi-arch frontend), which would make DefaultSpec() lie.
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

	targetPlat := platforms.Normalize(buildPlat)
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

        if samePlatform(targetPlat, buildPlat) {
                return targetBase.Run(
                        dalec.WithConstraints(append(opts, llb.Platform(targetPlat))...),
                        cfg.Install(cfg.BuilderPackages),
                ).Root()
        }

	// tdnf-based distros (Azure Linux / CBL-Mariner) do not have a reliable
        // cross-installroot flow (no --forcearch), so fall back to running the
        // package manager on the target platform under qemu.
        if cfg.CacheDir == "/var/cache/tdnf" {
                return targetBase.Run(
                        dalec.WithConstraints(append(opts, llb.Platform(targetPlat))...),
                        cfg.Install(cfg.BuilderPackages),
                ).Root()
        }

        targetArch, err := rpmArchFromPlatform(targetPlat)
        if err != nil {
                return dalec.ErrorState(llb.Scratch(), err)
        }

        buildBase := frontend.GetBaseImage(buildSOpt, cfg.ImageRef, buildOpts...).Platform(buildPlat)
        const rootfsMount = "/tmp/dalec/rootfs"

        es := buildBase.Run(
                dalec.WithConstraints(append(opts, llb.Platform(buildPlat))...),
                cfg.InstallIntoRoot(rootfsMount, cfg.BuilderPackages, targetArch, buildPlat),
        )
        es.AddMount(rootfsMount, targetBase)
        return es.GetMount(rootfsMount).Platform(targetPlat)

}

func (cfg *Config) SysextWorker(sOpts dalec.SourceOpts, opts ...llb.ConstraintsOpt) llb.State {
	buildPlat := platforms.Normalize(platforms.DefaultSpec())
        worker := cfg.workerWithBuildPlatform(sOpts, buildPlat, opts...)

        targetPlat := platforms.Normalize(buildPlat)
        if sOpts.TargetPlatform != nil {
               targetPlat = platforms.Normalize(*sOpts.TargetPlatform)
        }

        if samePlatform(targetPlat, buildPlat) {
                return worker.Run(
                        dalec.WithConstraints(append(opts, llb.Platform(targetPlat))...),
                        cfg.Install([]string{"erofs-utils"}),
                ).Root()
        }

        if cfg.CacheDir == "/var/cache/tdnf" {
                return worker.Run(
                        dalec.WithConstraints(append(opts, llb.Platform(targetPlat))...),
                        cfg.Install([]string{"erofs-utils"}),
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
        es := buildBase.Run(
                dalec.WithConstraints(append(opts, llb.Platform(buildPlat))...),
                cfg.InstallIntoRoot(rootfsMount, []string{"erofs-utils"}, targetArch, buildPlat),
        )

        es.AddMount(rootfsMount, worker)
        return es.GetMount(rootfsMount).Platform(targetPlat)
}
