package frontend

import (
	"bytes"
	"context"
	stderrors "errors"
	"fmt"
	"runtime"
	"time"

	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/project-dalec/dalec"
)

type LoadConfig struct {
	SubstituteOpts []dalec.SubstituteOpt
}

type LoadOpt func(*LoadConfig)

type frontendClient interface {
	CurrentFrontend() (*llb.State, error)
}

func WithAllowArgs(args ...string) LoadOpt {
	return func(cfg *LoadConfig) {
		set := make(map[string]struct{}, len(args))
		for _, arg := range args {
			set[arg] = struct{}{}
		}
		cfg.SubstituteOpts = append(cfg.SubstituteOpts, func(cfg *dalec.SubstituteConfig) {
			orig := cfg.AllowArg

			cfg.AllowArg = func(key string) bool {
				if orig != nil && orig(key) {
					return true
				}
				_, ok := set[key]
				return ok
			}
		})
	}
}

func LoadSpec(ctx context.Context, client *dockerui.Client, platform *ocispecs.Platform, opts ...LoadOpt) (*dalec.Spec, error) {
	cfg := LoadConfig{}

	for _, o := range opts {
		o(&cfg)
	}

	src, err := client.ReadEntrypoint(ctx, "dalec")
	if err != nil {
		return nil, fmt.Errorf("could not read spec file: %w", err)
	}

	data := bytes.TrimSpace(src.Data)
	spec, err := dalec.LoadSpecWithSourceMap(src.Filename, data)
	if err != nil {
		return nil, errors.Wrap(err, "error loading spec")
	}

	args := dalec.DuplicateMap(client.BuildArgs)
	if platform == nil {
		p := platforms.DefaultSpec()
		platform = &p
	}

	fillPlatformArgs("TARGET", args, *platform)
	fillPlatformArgs("BUILD", args, client.BuildPlatforms[0])

	if err := spec.SubstituteArgs(args, cfg.SubstituteOpts...); err != nil {
		return nil, errors.Wrap(err, "error resolving build args")
	}
	return spec, nil
}

func getOS(platform ocispecs.Platform) string {
	return platform.OS
}

func getArch(platform ocispecs.Platform) string {
	return platform.Architecture
}

func getVariant(platform ocispecs.Platform) string {
	return platform.Variant
}

func getPlatformFormat(platform ocispecs.Platform) string {
	return platforms.Format(platform)
}

var passthroughGetters = map[string]func(ocispecs.Platform) string{
	"OS":       getOS,
	"ARCH":     getArch,
	"VARIANT":  getVariant,
	"PLATFORM": getPlatformFormat,
}

func fillPlatformArgs(prefix string, args map[string]string, platform ocispecs.Platform) {
	for attr, getter := range passthroughGetters {
		args[prefix+attr] = getter(platform)
	}
}

type PlatformBuildFunc func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error)

// BuildWithPlatform is a helper function to build a spec with a given platform
// It takes care of looping through each target platform and executing the build with the platform args substituted in the spec.
// This also deals with the docker-style multi-platform output.
func BuildWithPlatform(ctx context.Context, client gwclient.Client, f PlatformBuildFunc) (*gwclient.Result, error) {
	dc, err := dockerui.NewClient(client)
	if err != nil {
		return nil, err
	}
	return BuildWithPlatformFromUIClient(ctx, client, dc, f)
}

func getPanicStack() error {
	stackBuf := make([]uintptr, 32)
	n := runtime.Callers(4, stackBuf) // Skip 4 frames to exclude runtime.Callers, the current function, and defer internals
	stackBuf = stackBuf[:n]
	frames := runtime.CallersFrames(stackBuf)
	var stackTrace string
	for {
		frame, more := frames.Next()
		stackTrace += fmt.Sprintf("%s\n\t%s:%d\n", frame.Function, frame.File, frame.Line)
		if !more {
			break
		}
	}
	return stderrors.New(stackTrace)
}

// Like [BuildWithPlatform] but with a pre-initialized dockerui.Client
func BuildWithPlatformFromUIClient(ctx context.Context, client gwclient.Client, dc *dockerui.Client, f PlatformBuildFunc) (*gwclient.Result, error) {
	rb, err := dc.Build(ctx, func(ctx context.Context, platform *ocispecs.Platform, idx int) (_ gwclient.Reference, _ *dalec.DockerImageSpec, _ *dalec.DockerImageSpec, retErr error) {
		defer func() {
			if r := recover(); r != nil {
				trace := getPanicStack()
				recErr := fmt.Errorf("recovered from panic in build: %+v", r)
				retErr = stderrors.Join(recErr, trace)
			}
		}()

		spec, err := LoadSpec(ctx, dc, platform)
		if err != nil {
			return nil, nil, nil, err
		}

		targetKey := GetTargetKey(dc)

		ref, cfg, err := f(ctx, client, platform, spec, targetKey)
		if cfg != nil {
			now := time.Now()
			cfg.Created = &now
		}
		return ref, cfg, nil, err
	})
	if err != nil {
		return nil, err
	}
	return rb.Finalize()
}

// GetBaseImage returns an image that first checks if the client provided the
// image in the build context matching the image ref.
//
// This follows the behavior of of the dockerfile frontend.
func GetBaseImage(sOpt dalec.SourceOpts, ref string, opts ...llb.ConstraintsOpt) llb.State {
	return llb.Scratch().Async(func(ctx context.Context, _ llb.State, c *llb.Constraints) (llb.State, error) {
		for _, o := range opts {
			o.SetConstraintsOption(c)
		}

		fromClient, err := sOpt.GetContext(ref, dalec.WithConstraint(c))
		if err != nil {
			return llb.Scratch(), err
		}

		if fromClient != nil {
			return *fromClient, nil
		}

		return llb.Image(ref, dalec.WithConstraint(c), llb.WithMetaResolver(sOpt.Resolver)), nil
	})
}

// WithDefaultPlatform is a helper function to set a default platform for a build
// if the client does not provide one.
func WithDefaultPlatform(platform ocispecs.Platform, build gwclient.BuildFunc) gwclient.BuildFunc {
	return func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
		if client.BuildOpts().Opts["platform"] != "" {
			return build(ctx, client)
		}
		client = &clientWithPlatform{
			Client:   client,
			platform: &platform,
		}
		return build(ctx, client)
	}
}

type clientWithPlatform struct {
	gwclient.Client
	platform *ocispecs.Platform
}

func (c *clientWithPlatform) BuildOpts() gwclient.BuildOpts {
	opts := c.Client.BuildOpts()
	opts.Opts["platform"] = platforms.Format(*c.platform)
	return opts
}

func GetCurrentFrontend(client gwclient.Client) llb.State {
	return *getCurrentFrontend(client)
}

func getCurrentFrontend(client gwclient.Client) *llb.State {
	f, err := client.(frontendClient).CurrentFrontend()
	if err != nil {
		panic(err)
	}

	if f == nil {
		panic("nil frontend state returned -- this should never happen")
	}

	return f
}

func withCredHelper(c gwclient.Client) func() (llb.RunOption, error) {
	return func() (llb.RunOption, error) {
		f := GetCurrentFrontend(c)
		return dalec.RunOptFunc(func(ei *llb.ExecInfo) {
			llb.AddMount("/usr/local/bin/frontend", f, llb.SourcePath("/frontend")).SetRunOption(ei)
		}), nil
	}
}
