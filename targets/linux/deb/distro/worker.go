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

func samePlatform(a, b ocispecs.Platform) bool {
	// Normalize to avoid false mismatches like:
	//   linux/arm64  vs linux/arm64/v8
	// and other canonicalization differences.
	na := platforms.Normalize(a)
	nb := platforms.Normalize(b)

	// Only() performs sensible matching/normalization rules for platform tuples.
	// Symmetric match keeps this closer to "equivalence" than "compatibility".
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
		p := platforms.Normalize(platforms.DefaultSpec())
		if normPlatform != nil {
			p = *normPlatform
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
	// Determine target platform (requested) and build platform (host/native).
	buildPlat := platforms.Normalize(platforms.DefaultSpec())
	targetPlat := buildPlat
	if sOpt.TargetPlatform != nil {
		targetPlat = platforms.Normalize(*sOpt.TargetPlatform)
	}

	// IMPORTANT: Base rootfs must be target platform.
	// Pin the image state to the target platform so the mounted rootfs is actually target-arch.
	targetBase := frontend.GetBaseImage(sOpt, cfg.ImageRef, opts...).Platform(targetPlat)

	// Native build: keep current behavior.
	if samePlatform(targetPlat, buildPlat) {
		return targetBase.Run(
			dalec.WithConstraints(append(opts, llb.Platform(targetPlat))...),
			AptInstall(cfg.BuilderPackages, opts...),
			dalec.WithMountedAptCache(cfg.AptCachePrefix),
		).Root()
	}

	targetArch, err := debArchFromPlatform(targetPlat)
	if err != nil {
		return dalec.ErrorState(llb.Scratch(), err)
	}

	// Build platform container (tools run here), pinned to build platform.
	buildBase := frontend.GetBaseImage(sOpt, cfg.ImageRef, opts...).Platform(buildPlat)
	const rootfsMount = "/tmp/dalec/rootfs"
	cacheKey := aptCacheKeyForCross(cfg.AptCachePrefix, targetPlat)

	es := buildBase.Run(
		dalec.WithConstraints(append(opts, llb.Platform(buildPlat))...),
		AptInstallIntoRoot(rootfsMount, cfg.BuilderPackages, targetArch, opts...),
		dalec.WithMountedAptCache(cacheKey),
	)

	es.AddMount(rootfsMount, targetBase)
	return es.GetMount(rootfsMount)

}

func (cfg *Config) SysextWorker(sOpts dalec.SourceOpts, opts ...llb.ConstraintsOpt) llb.State {
	// Reuse Worker logic (which may already be cross-optimized), then add sysext deps.
	worker := cfg.Worker(sOpts, opts...)

	buildPlat := platforms.Normalize(platforms.DefaultSpec())
	targetPlat := buildPlat
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
	buildBase := frontend.GetBaseImage(sOpts, cfg.ImageRef, opts...).Platform(buildPlat)
	const rootfsMount = "/tmp/dalec/rootfs"
	cacheKey := aptCacheKeyForCross(cfg.AptCachePrefix, targetPlat)

	es := buildBase.Run(
		dalec.WithConstraints(append(opts, llb.Platform(buildPlat))...),
		AptInstallIntoRoot(rootfsMount, []string{"erofs-utils"}, targetArch, opts...),
		dalec.WithMountedAptCache(cacheKey),
	)

	es.AddMount(rootfsMount, worker)
	return es.GetMount(rootfsMount)

}
