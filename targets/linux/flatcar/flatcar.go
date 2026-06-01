package flatcar

import (
	"context"
	"fmt"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests/targets"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
	"github.com/project-dalec/dalec/targets/linux"
	"github.com/project-dalec/dalec/targets/linux/deb/ubuntu"
)

const TargetKey = "flatcar"

type Config struct {
	Base linux.DistroConfig
}

type workerHandler interface {
	HandleWorker(context.Context, gwclient.Client) (*gwclient.Result, error)
}

var DefaultConfig = &Config{
	Base: ubuntu.NobleConfig,
}

// SysextEnv provides Flatcar-friendly defaults.
// Build args with the DALEC_SYSEXT_* prefix still override these.
func (c *Config) SysextEnv(spec *dalec.Spec, targetKey string) map[string]string {
	return map[string]string{
		"DALEC_SYSEXT_OS_ID":        "flatcar",
		"DALEC_SYSEXT_SYSEXT_LEVEL": "1.0",
		// Flatcar expects /etc/extensions/<name>.raw, so output "<name>.raw".
		"DALEC_SYSEXT_IMAGE_NAME": spec.Name,
	}
}

func (c *Config) Handle(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	var mux frontend.BuildMux
	mux.Add("testing/sysext", linux.HandleSysext(c), &targets.Target{
		Name:        "testing/sysext",
		Description: "Build a Flatcar-compatible systemd sysext (.raw)",
		Default:     true,
	})
	mux.Add("worker", c.HandleWorker, &targets.Target{
		Name:        "worker",
		Description: "Builds the worker image used to assemble Flatcar sysext images.",
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
	return c.Base.SysextWorker(sOpt, opts...)
}

func (c *Config) HandleWorker(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	h, ok := c.Base.(workerHandler)
	if !ok {
		return nil, fmt.Errorf("flatcar base %T does not provide a worker target", c.Base)
	}
	return h.HandleWorker(ctx, client)
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

func (c *Config) RunTests(ctx context.Context, client gwclient.Client, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, opts ...llb.ConstraintsOpt) llb.StateOption {
	return c.Base.RunTests(ctx, client, sOpt, spec, targetKey, opts...)
}
