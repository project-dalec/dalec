package flatcar

import (
	"context"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests/targets"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
	"github.com/project-dalec/dalec/targets/linux"
	"github.com/project-dalec/dalec/targets/linux/deb/ubuntu"
)

const TargetKey = "flatcar"

// Default SDK image. Keep it overrideable by code later if you want.
const DefaultSDKImage = "ghcr.io/flatcar/flatcar-sdk-all:4593.0.0"

type Config struct {
	Base        linux.DistroConfig
	SDKImageRef string
}

var DefaultConfig = &Config{
	Base:        ubuntu.NobleConfig,
	SDKImageRef: DefaultSDKImage,
}

// SysextEnv provides Flatcar-friendly defaults.
// build-args DALEC_SYSEXT_* still override these (your merge logic).
func (c *Config) SysextEnv(spec *dalec.Spec, targetKey string) map[string]string {
	return map[string]string{
		"DALEC_SYSEXT_OS_ID":        "flatcar",
		"DALEC_SYSEXT_SYSEXT_LEVEL": "1.0",
		// Flatcar expects /etc/extensions/<name>.raw → output "<name>.raw"
		"DALEC_SYSEXT_IMAGE_NAME": spec.Name,
	}
}

func (c *Config) Handle(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	var mux frontend.BuildMux
	mux.Add("testing/sysext", linux.HandleSysext(c), &targets.Target{
		Name:        "testing/sysext",
		Description: "Build a Flatcar-compatible systemd sysext (.raw) using Flatcar SDK",
		Default:     true,
	})
	return mux.Handle(ctx, client)
}

// ---- linux.DistroConfig delegation ----

func (c *Config) Validate(spec *dalec.Spec) error {
	return c.Base.Validate(spec)
}

func (c *Config) Worker(sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) llb.State {
	return c.Base.Worker(sOpt, opts...)
}

func (c *Config) SysextWorker(sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) llb.State {
	ref := c.SDKImageRef
	if ref == "" {
		ref = DefaultSDKImage
	}
	return frontend.GetBaseImage(sOpt, ref, opts...)
}

func (c *Config) BuildPkg(ctx context.Context, client gwclient.Client, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, opts ...llb.ConstraintsOpt) llb.State {
	return c.Base.BuildPkg(ctx, client, sOpt, spec, targetKey, opts...)
}

func (c *Config) ExtractPkg(ctx context.Context, client gwclient.Client, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, pkgState llb.State, opts ...llb.ConstraintsOpt) llb.State {
	return c.Base.ExtractPkg(ctx, client, sOpt, spec, targetKey, pkgState, opts...)
}

func (c *Config) BuildContainer(ctx context.Context, client gwclient.Client, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, pkgState llb.State, opts ...llb.ConstraintsOpt) llb.State {
	return c.Base.BuildContainer(ctx, client, sOpt, spec, targetKey, pkgState, opts...)
}

func (c *Config) RunTests(ctx context.Context, client gwclient.Client, spec *dalec.Spec, sOpt dalec.SourceOpts, ctr llb.State, targetKey string, opts ...llb.ConstraintsOpt) llb.StateOption {
	return c.Base.RunTests(ctx, client, spec, sOpt, ctr, targetKey, opts...)
}
