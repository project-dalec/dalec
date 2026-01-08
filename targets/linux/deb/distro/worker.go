package distro

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
)

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

// samePlatform reports whether two OCI platforms are effectively equivalent.
// It uses a symmetric match (both directions) to avoid treating merely
// compatible platforms as the same (e.g. linux/arm64 vs linux/arm64/v8).
func samePlatform(a, b ocispecs.Platform) bool {
	na := platforms.Normalize(a)
	nb := platforms.Normalize(b)

	return platforms.Only(na).Match(nb) && platforms.Only(nb).Match(na)
}

func debArchFromPlatform(p ocispecs.Platform) (string, error) {
	switch p.Architecture {
	case "amd64":
		return "amd64", nil
	case "386":
		return "i386", nil
	case "arm64":
		return "arm64", nil
	case "arm":
		// Common Debian naming:
		//  - arm/v7 => armhf
		//  - arm/v6 => armel (best-effort)
		// NOTE: p is expected to be platforms.Normalize()'d by callers;
		// ARM variants (v6/v7) are canonicalized at that stage.
		switch p.Variant {
		case "v7", "":
			return "armhf", nil
		case "v6":
			return "armel", nil
		default:
			return "", errors.Errorf("unsupported arm variant for deb: %q", p.Variant)
		}
	default:
		return "", errors.Errorf("unsupported platform arch for deb: %q", p.Architecture)
	}
}

// aptCacheKeyForCross returns a platform-scoped apt cache key for cross-architecture builds.
// When building a target image on a different build platform, the shared apt cache must be
// separated by target platform to avoid mixing build-arch and target-arch .deb artifacts.
func aptCacheKeyForCross(prefix string, target ocispecs.Platform) string {
	if prefix == "" {
		return prefix
	}
	// platforms.Format => e.g. "linux/arm64"
	s := platforms.Format(target)
	s = strings.NewReplacer("/", "_", ":", "_").Replace(s)
	return prefix + "-" + s
}

func (cfg *Config) HandleWorker(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		// Normalize the platform early so:
		// - build-vs-target comparisons are consistent
		// - llb.Platform(...) is canonical
		// - ResolveImageConfig gets the canonical platform
		var normPlatform *ocispecs.Platform
		if platform != nil {
			p := platforms.Normalize(*platform)
			normPlatform = &p
		}
		sOpt, err := frontend.SourceOptFromClient(ctx, client, normPlatform)
		if err != nil {
			return nil, nil, err
		}

		buildPlat := nativeExecutorPlatform(client)
		p := platforms.Normalize(platforms.DefaultSpec())
		if normPlatform != nil {
			p = *normPlatform
		}
		pc := llb.Platform(p)

		ignoreCache := frontend.IgnoreCache(client, cfg.ImageRef, cfg.ContextRef)
		st := cfg.workerWithBuildPlatform(sOpt, buildPlat, pc, ignoreCache)

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
				Platform: normPlatform,
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
	buildPlat := platforms.Normalize(platforms.DefaultSpec())
	return cfg.workerWithBuildPlatform(sOpt, buildPlat, opts...)
}

// workerWithBuildPlatform builds the worker image for the requested target platform.
// buildPlat represents the native executor platform where build-time tools
// (e.g. apt, dpkg) are run, enabling cross-platform worker builds when it differs
// from the target platform.
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
	// Determine target platform (requested) and build platform (native executor).
	targetPlat := platforms.Normalize(buildPlat)
	if sOpt.TargetPlatform != nil {
		targetPlat = platforms.Normalize(*sOpt.TargetPlatform)
	}

	// IMPORTANT:
	// Resolve base images with explicit per-purpose SourceOpts so we don't accidentally
	// end up with an arm64 rootfs for the buildBase when --platform=arm64 is requested.
	targetSOpt := sOpt
	targetSOpt.TargetPlatform = &targetPlat
	buildSOpt := sOpt
	buildSOpt.TargetPlatform = &buildPlat

	// IMPORTANT:
	// Pass the platform constraint at base-image creation time.
	// Relying only on State.Platform(...) is not sufficient if GetBaseImage already resolved a rootfs.
	targetOpts := append(append([]llb.ConstraintsOpt{}, opts...), llb.Platform(targetPlat))
	buildOpts := append(append([]llb.ConstraintsOpt{}, opts...), llb.Platform(buildPlat))

	// IMPORTANT: Base rootfs must be target platform.
	// Pin the image state to the target platform so the mounted rootfs is actually target-arch.
	targetBase := frontend.GetBaseImage(targetSOpt, cfg.ImageRef, targetOpts...).Platform(targetPlat)

	// Native build: keep current behavior.
	if samePlatform(targetPlat, buildPlat) {
		return targetBase.Run(
			dalec.WithConstraints(append(opts, llb.Platform(targetPlat))...),
			AptInstall(cfg.BuilderPackages, opts...),
			dalec.WithMountedAptCache(cfg.AptCachePrefix, opts...),
		).Root()
	}

	targetArch, err := debArchFromPlatform(targetPlat)
	if err != nil {
		return dalec.ErrorState(llb.Scratch(), err)
	}

	// Build platform container (tools run here), pinned to build platform.
	buildBase := frontend.GetBaseImage(buildSOpt, cfg.ImageRef, buildOpts...).Platform(buildPlat)
	const rootfsMount = "/tmp/dalec/rootfs"
	cacheKey := aptCacheKeyForCross(cfg.AptCachePrefix, targetPlat)

	es := buildBase.Run(
		dalec.WithConstraints(append(opts, llb.Platform(buildPlat))...),
		llb.AddMount(rootfsMount, targetBase),
		AptInstallIntoRoot(rootfsMount, cfg.BuilderPackages, targetArch, buildPlat),
		dalec.WithMountedAptCache(cacheKey),
	)

	return es.GetMount(rootfsMount).Platform(targetPlat)
}

func (cfg *Config) SysextWorker(sOpts dalec.SourceOpts, opts ...llb.ConstraintsOpt) llb.State {
	// Build the base worker using the same cross-safe logic as Worker.
	// NOTE: SysextWorker can be invoked via a different mux path than HandleWorker,
	// so we must not rely on BuildKit worker enumeration here.
	buildPlat := platforms.Normalize(platforms.DefaultSpec())
	worker := cfg.workerWithBuildPlatform(sOpts, buildPlat, opts...)

	targetPlat := platforms.Normalize(buildPlat)
	if sOpts.TargetPlatform != nil {
		targetPlat = platforms.Normalize(*sOpts.TargetPlatform)
	}

	// Native: install directly.
	if samePlatform(targetPlat, buildPlat) {
		return worker.Run(
			dalec.WithConstraints(append(opts, llb.Platform(targetPlat))...),
			AptInstall([]string{"erofs-utils"}, opts...),
			dalec.WithMountedAptCache(cfg.AptCachePrefix),
		).Root()
	}

	targetArch, err := debArchFromPlatform(targetPlat)
	if err != nil {
		return dalec.ErrorState(llb.Scratch(), err)
	}

	// Run tools on build platform; mount/mutate the target worker rootfs.
	// IMPORTANT: Resolve base images with explicit per-purpose SourceOpts + constraints.
	buildSOpt := sOpts
	buildSOpt.TargetPlatform = &buildPlat
	buildOpts := append(append([]llb.ConstraintsOpt{}, opts...), llb.Platform(buildPlat))

	const rootfsMount = "/tmp/dalec/rootfs"
	cacheKey := aptCacheKeyForCross(cfg.AptCachePrefix, targetPlat)

	buildBase := frontend.GetBaseImage(buildSOpt, cfg.ImageRef, buildOpts...).Platform(buildPlat)
	es := buildBase.Run(
		dalec.WithConstraints(append(opts, llb.Platform(buildPlat))...),
		llb.AddMount(rootfsMount, worker),
		AptInstallIntoRoot(rootfsMount, []string{"erofs-utils"}, targetArch, buildPlat),
		dalec.WithMountedAptCache(cacheKey),
	)

	return es.GetMount(rootfsMount).Platform(targetPlat)
}
