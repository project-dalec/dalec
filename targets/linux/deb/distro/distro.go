package distro

import (
	"context"
	"encoding/json"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	bktargets "github.com/moby/buildkit/frontend/subrequests/targets"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
	"github.com/project-dalec/dalec/targets/linux"
)

var (
	defaultRepoConfig = &dalec.RepoPlatformConfig{
		ConfigRoot: "/etc/apt/sources.list.d",
		GPGKeyRoot: "/usr/share/keyrings",
		ConfigExt:  ".list",
	}
)

type Config struct {
	ImageRef       string
	ContextRef     string
	VersionID      string
	AptCachePrefix string

	BuilderPackages    []string
	BasePackages       []string
	RepoPlatformConfig *dalec.RepoPlatformConfig

	DefaultOutputImage string

	// ExtraRepos is used by distributions that want to enable extra repositories
	// that are not in the base worker config.
	// A prime example of this is adding Debian backports on debian distributions.
	ExtraRepos []dalec.PackageRepositoryConfig

	// erofs-utils 1.7+ is required for tar support.
	SysextSupported bool
}

func (cfg *Config) BuildImageConfig(ctx context.Context, sOpt dalec.SourceOpts, spec *dalec.Spec, platform *ocispecs.Platform, targetKey string) (*dalec.DockerImageSpec, error) {
	img, err := resolveConfig(ctx, sOpt, spec, platform, targetKey)
	if err != nil {
		return nil, err
	}

	if err := dalec.BuildImageConfig(spec, targetKey, img); err != nil {
		return nil, err
	}

	return img, nil
}

func resolveConfig(ctx context.Context, sOpt dalec.SourceOpts, spec *dalec.Spec, platform *ocispecs.Platform, targetKey string) (*dalec.DockerImageSpec, error) {
	bi, err := spec.GetSingleBase(targetKey)
	if err != nil {
		return nil, err
	}

	if bi == nil {
		return dalec.BaseImageConfig(platform), nil
	}

	dt, err := bi.ResolveImageConfig(ctx, sOpt, sourceresolver.Opt{
		ImageOpt: &sourceresolver.ResolveImageOpt{
			Platform: platform,
		},
	})
	if err != nil {
		return nil, errors.Wrap(err, "error resolving base image config")
	}

	var img dalec.DockerImageSpec
	if err := json.Unmarshal(dt, &img); err != nil {
		return nil, errors.Wrap(err, "error unmarshalling base image config")
	}
	return &img, nil
}

func (cfg *Config) RepoMounts(repos []dalec.PackageRepositoryConfig, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) llb.RunOption {
	opts = append(opts, dalec.ProgressGroup("Prepare custom repos"))

	repoConfig := cfg.RepoPlatformConfig
	if repoConfig == nil {
		repoConfig = defaultRepoConfig
	}

	withRepos := dalec.WithRepoConfigs(repos, repoConfig, sOpt, opts...)
	withData := dalec.WithRepoData(repos, sOpt, opts...)
	keyMounts, _ := dalec.GetRepoKeys(repos, repoConfig, sOpt, opts...)

	return dalec.WithRunOptions(withRepos, withData, keyMounts)
}

// Routes returns the flat routes for this DEB distro config, prefixed with the given prefix.
func (cfg *Config) Routes(prefix string, spec *dalec.Spec) ([]frontend.Route, error) {
	// Check whether this target key is defined in the spec.
	_, specDefined := spec.Targets[prefix]
	specDefined = specDefined && len(spec.Targets) > 0

	routes := []frontend.Route{
		{
			FullPath: prefix,
			Handler:  linux.HandlePackage(cfg),
			Info: frontend.Target{
				Target: bktargets.Target{
					Name:        prefix,
					Description: "Builds a deb package.",
				},
				SpecDefined: specDefined,
				Hidden:      true,
			},
		},
		{
			FullPath: prefix + "/deb",
			Handler:  linux.HandlePackage(cfg),
			Info: frontend.Target{
				Target: bktargets.Target{
					Name:        prefix + "/deb",
					Description: "Builds a deb package.",
					Default:     true,
				},
				SpecDefined: specDefined,
			},
		},
		{
			FullPath: prefix + "/testing/container",
			Handler:  linux.HandleContainer(cfg),
			Info: frontend.Target{
				Target: bktargets.Target{
					Name:        prefix + "/testing/container",
					Description: "Builds a container image for testing purposes only.",
				},
				SpecDefined: specDefined,
			},
		},
		{
			FullPath: prefix + "/dsc",
			Handler:  cfg.HandleSourcePkg,
			Info: frontend.Target{
				Target: bktargets.Target{
					Name:        prefix + "/dsc",
					Description: "Builds a Debian source package.",
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
					Description: "Builds the worker image.",
				},
				SpecDefined: specDefined,
			},
		},
	}

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
