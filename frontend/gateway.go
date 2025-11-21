package frontend

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/bklog"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/project-dalec/dalec"
)

const (
	requestIDKey               = "requestid"
	dalecSubrequstForwardBuild = "dalec.forward.build"

	gatewayFrontend = "gateway.v0"
)

func getDockerfile(ctx context.Context, client gwclient.Client, build *dalec.SourceBuild, defPb *pb.Definition) ([]byte, error) {
	dockerfilePath := dockerui.DefaultDockerfileName

	if build.DockerfilePath != "" {
		dockerfilePath = build.DockerfilePath
	}

	// First we need to read the dockerfile to determine what frontend to forward to
	res, err := client.Solve(ctx, gwclient.SolveRequest{
		Definition: defPb,
	})
	if err != nil {
		return nil, errors.Wrap(err, "error getting build context")
	}

	ref, err := res.SingleRef()
	if err != nil {
		return nil, err
	}

	dt, err := ref.ReadFile(ctx, gwclient.ReadRequest{
		Filename: dockerfilePath,
	})
	if err != nil {
		return nil, errors.Wrap(err, "error reading dockerfile")
	}
	return dt, nil
}

// ForwarderFromClient creates a [dalec.ForwarderFunc] from a gateway client.
// This is used for forwarding builds to other frontends in [dalec.Source2LLBGetter]
func ForwarderFromClient(ctx context.Context, client gwclient.Client) dalec.ForwarderFunc {
	return func(st llb.State, spec *dalec.SourceBuild, opts ...llb.ConstraintsOpt) (llb.State, error) {
		if spec == nil {
			spec = &dalec.SourceBuild{}
		}

		def, err := st.Marshal(ctx, opts...)
		if err != nil {
			return llb.Scratch(), err
		}
		defPb := def.ToPB()

		dockerfileDt, err := getDockerfile(ctx, client, spec, defPb)
		if err != nil {
			return llb.Scratch(), err
		}

		req, err := newSolveRequest(
			toDockerfile(ctx, st, dockerfileDt, spec, dalec.ProgressGroup("prepare dockerfile to forward to frontend")),
			copyForForward(ctx, client),
		)
		if err != nil {
			return llb.Scratch(), err
		}

		res, err := client.Solve(ctx, req)
		if err != nil {
			return llb.Scratch(), err
		}
		ref, err := res.SingleRef()
		if err != nil {
			return llb.Scratch(), err
		}
		return ref.ToState()
	}
}

func GetBuildArg(client gwclient.Client, k string) (string, bool) {
	opts := client.BuildOpts().Opts
	if opts != nil {
		if v, ok := opts["build-arg:"+k]; ok {
			return v, true
		}
	}
	return "", false
}

func SourceOptFromUIClient(ctx context.Context, c gwclient.Client, dc *dockerui.Client, platform *ocispecs.Platform) dalec.SourceOpts {
	return dalec.SourceOpts{
		TargetPlatform: platform,
		Resolver:       c,
		Forward:        ForwarderFromClient(ctx, c),
		GetContext: func(ref string, opts ...llb.LocalOption) (*llb.State, error) {
			if ref == dockerui.DefaultLocalNameContext {
				return dc.MainContext(ctx, opts...)
			}
			nc, err := dc.NamedContext(ref, dockerui.ContextOpt{
				ResolveMode: dc.ImageResolveMode.String(),
				AsyncLocalOpts: func() []llb.LocalOption {
					return opts
				},
				Platform: platform,
			})
			if err != nil {
				return nil, err
			}
			if nc == nil {
				return nil, nil
			}
			st, _, err := nc.Load(ctx)
			return st, err
		},
		GitCredHelperOpt: withCredHelper(c),
	}
}

func SourceOptFromClient(ctx context.Context, c gwclient.Client, platform *ocispecs.Platform) (dalec.SourceOpts, error) {
	dc, err := dockerui.NewClient(c)
	if err != nil {
		return dalec.SourceOpts{}, err
	}
	return SourceOptFromUIClient(ctx, c, dc, platform), nil
}

var (
	supportsDiffMergeOnce sync.Once
	supportsDiffMerge     atomic.Bool
)

// SupportsDiffMerge checks if the given client supports the diff and merge operations.
func SupportsDiffMerge(client gwclient.Client) bool {
	supportsDiffMergeOnce.Do(func() {
		if client.BuildOpts().Opts["build-arg:DALEC_DISABLE_DIFF_MERGE"] == "1" {
			supportsDiffMerge.Store(false)
			return
		}
		supportsDiffMerge.Store(checkDiffMerge(client))
	})
	return supportsDiffMerge.Load()
}

func checkDiffMerge(client gwclient.Client) bool {
	caps := client.BuildOpts().LLBCaps
	if caps.Supports(pb.CapMergeOp) != nil {
		return false
	}

	if caps.Supports(pb.CapDiffOp) != nil {
		return false
	}
	return true
}

// copyForForward copies all the inputs and build opts from the initial request in order to forward to another frontend.
func copyForForward(ctx context.Context, client gwclient.Client) solveRequestOpt {
	return func(req *gwclient.SolveRequest) error {
		// Inputs are any additional build contexts or really any llb that the client sent along.
		inputs, err := client.Inputs(ctx)
		if err != nil {
			return err
		}

		if req.FrontendInputs == nil {
			req.FrontendInputs = make(map[string]*pb.Definition, len(inputs))
		}

		for k, v := range inputs {
			if _, ok := req.FrontendInputs[k]; ok {
				// Do not overwrite existing inputs
				continue
			}

			def, err := v.Marshal(ctx)
			if err != nil {
				return errors.Wrap(err, "error marshaling frontend input")
			}
			req.FrontendInputs[k] = def.ToPB()
		}

		opts := client.BuildOpts().Opts
		if req.FrontendOpt == nil {
			req.FrontendOpt = make(map[string]string, len(opts))
		}

		for k, v := range opts {

			if k == "filename" || k == "dockerfilekey" || k == "target" {
				// These are some well-known keys that the dockerfile frontend uses
				// which we'll be overriding with our own values (as needed) in the
				// caller.
				// Basically there should not be a need, nor is it desirable, to forward these along.
				continue
			}

			if _, ok := req.FrontendOpt[k]; ok {
				// Do not overwrite existing opts
				continue
			}
			req.FrontendOpt[k] = v
		}

		return nil
	}
}

const keyTopLevelTarget = "dalec.target"

type BuildOpstGetter interface {
	BuildOpts() gwclient.BuildOpts
}

// GetTargetKey returns the key that should be used to select the [dalec.Target] from the [dalec.Spec]
func GetTargetKey(client BuildOpstGetter) string {
	return client.BuildOpts().Opts[keyTopLevelTarget]
}

// Warn sends a warning to the client for the provided state.
func Warn(ctx context.Context, client gwclient.Client, st llb.State, msg string) {
	// Note: This will attempt to marshal the state to get its digest for metadata
	// on the warning message, but it is not required to actually write the message.
	// For this reason we can continue on error.

	def, err := st.Marshal(ctx)
	if err != nil {
		bklog.G(ctx).WithError(err).WithField("warn", msg).Warn("Error marshalling state for outputting warning message")
	}

	var dgst digest.Digest
	if def != nil {
		dgst, err = def.Head()
		if err != nil {
			bklog.G(ctx).WithError(err).WithField("warn", msg).Warn("Could not get state digest for outputting warning message")
		}
	}

	if err := client.Warn(ctx, dgst, msg, gwclient.WarnOpts{}); err != nil {
		bklog.G(ctx).WithError(err).WithField("warn", msg).Warn("Error writing warning message")
	}
}

func Warnf(ctx context.Context, client gwclient.Client, st llb.State, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	Warn(ctx, client, st, msg)
}
