package distro

import (
	"context"
	"encoding/json"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests/targets"
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
		Platform: platform,
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

func (cfg *Config) Handle(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	var mux frontend.BuildMux

	mux.Add("deb", linux.HandlePackage(cfg), &targets.Target{
		Name:        "deb",
		Description: "Builds a deb package.",
		Default:     true,
	})

	mux.Add("testing/container", linux.HandleContainer(cfg), &targets.Target{
		Name:        "testing/container",
		Description: "Builds a container image for testing purposes only.",
	})

	mux.Add("dsc", cfg.HandleSourcePkg, &targets.Target{
		Name:        "dsc",
		Description: "Builds a Debian source package.",
	})

	mux.Add("worker", cfg.HandleWorker, &targets.Target{
		Name:        "worker",
		Description: "Builds the worker image.",
	})

	if cfg.SysextSupported {
		mux.Add("testing/sysext", linux.HandleSysext(cfg), &targets.Target{
			Name:        "testing/sysext",
			Description: "Builds a systemd system extension image.",
		})
	}

	return mux.Handle(ctx, client)
}
