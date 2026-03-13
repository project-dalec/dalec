package distro

import (
	"path/filepath"

	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	bktargets "github.com/moby/buildkit/frontend/subrequests/targets"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
	"github.com/project-dalec/dalec/targets/linux"
)

type Config struct {
	FullName   string
	ImageRef   string
	ContextRef string

	// The release version of the distro
	ReleaseVer string

	// Build dependencies needed
	BuilderPackages []string

	// Dependencies to install in base image
	BasePackages       []dalec.Spec
	RepoPlatformConfig *dalec.RepoPlatformConfig

	DefaultOutputImage string

	InstallFunc PackageInstaller

	// Unique identifier for the package cache for this particular distro,
	// e.g., azlinux3-tdnf-cache
	CacheName string
	// Whether to namespace the cache key by platform
	// Not all distros need this, hence why it is configurable.
	CacheAddPlatform bool

	// Cache directories to mount (e.g. ["/var/cache/dnf"] or ["/var/cache/tdnf", "/var/cache/dnf"])
	CacheDir []string

	// erofs-utils 1.7+ is required for tar support.
	SysextSupported bool
}

func (cfg *Config) PackageCacheMount(root string) llb.RunOption {
	return dalec.RunOptFunc(func(ei *llb.ExecInfo) {
		cacheKey := cfg.CacheName
		if cfg.CacheAddPlatform {
			p := ei.Constraints.Platform
			if p == nil {
				p = ei.Platform
			}
			if p == nil {
				dp := platforms.DefaultSpec()
				p = &dp
			}
			cacheKey += "-" + platforms.Format(*p)
		}

		if len(cfg.CacheDir) == 0 {
			return
		}

		// Mount each cache dir. If there are multiple, suffix key with the dir base.
		for _, d := range cfg.CacheDir {
			if d == "" {
				continue
			}
			k := cacheKey
			if len(cfg.CacheDir) > 1 {
				k = cacheKey + "-" + filepath.Base(d)
			}
			llb.AddMount(
				joinUnderRoot(root, d),
				llb.Scratch(),
				llb.AsPersistentCacheDir(k, llb.CacheMountLocked),
			).SetRunOption(ei)
		}

	})
}

func (c *Config) Install(pkgs []string, opts ...DnfInstallOpt) llb.RunOption {
	var cfg dnfInstallConfig
	dnfInstallOptions(&cfg, opts)

	return dalec.WithRunOptions(c.InstallFunc(&cfg, c.ReleaseVer, pkgs), c.PackageCacheMount(cfg.root))
}

// Routes returns the flat routes for this RPM distro config, prefixed with the given prefix.
func (cfg *Config) Routes(prefix string, spec *dalec.Spec) ([]frontend.Route, error) {
	// Check whether this target key is defined in the spec.
	_, specDefined := spec.Targets[prefix]
	// Only mark SpecDefined when the spec actually defines targets.
	specDefined = specDefined && len(spec.Targets) > 0

	routes := []frontend.Route{
		{
			FullPath: prefix,
			Handler:  linux.HandleContainer(cfg),
			Info: frontend.Target{
				Target: bktargets.Target{
					Name:        prefix,
					Description: "Builds a container image for " + cfg.FullName,
				},
				SpecDefined: specDefined,
				Hidden:      true,
			},
		},
		{
			FullPath: prefix + "/rpm",
			Handler:  linux.HandlePackage(cfg),
			Info: frontend.Target{
				Target: bktargets.Target{
					Name:        prefix + "/rpm",
					Description: "Builds an rpm and src.rpm.",
				},
				SpecDefined: specDefined,
			},
		},
		{
			FullPath: prefix + "/container",
			Handler:  linux.HandleContainer(cfg),
			Info: frontend.Target{
				Target: bktargets.Target{
					Name:        prefix + "/container",
					Description: "Builds a container image for " + cfg.FullName,
					Default:     true,
				},
				SpecDefined: specDefined,
			},
		},
		{
			FullPath: prefix + "/container/depsonly",
			Handler:  cfg.HandleDepsOnly,
			Info: frontend.Target{
				Target: bktargets.Target{
					Name:        prefix + "/container/depsonly",
					Description: "Builds a container image with only the runtime dependencies installed.",
				},
				SpecDefined: specDefined,
			},
		},
		{
			FullPath: prefix + "/worker",
			Handler:  cfg.HandleWorker,
			Info: frontend.Target{
				Target: bktargets.Target{
					Name:        prefix + "/worker",
					Description: "Builds the base worker image responsible for building the rpm",
				},
				SpecDefined: specDefined,
			},
		},
	}

	// RPM debug sub-routes
	routes = append(routes, cfg.DebugRoutes(prefix+"/rpm/debug", specDefined)...)

	if cfg.SysextSupported {
		routes = append(routes, frontend.Route{
			FullPath: prefix + "/testing/sysext",
			Handler:  linux.HandleSysext(cfg),
			Info: frontend.Target{
				Target: bktargets.Target{
					Name:        prefix + "/testing/sysext",
					Description: "Builds a systemd system extension image.",
				},
				SpecDefined: specDefined,
			},
		})
	}

	return routes, nil
}
