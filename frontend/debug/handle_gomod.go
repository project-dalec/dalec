package debug

import (
	"context"
	"net"
	"runtime"
	"strings"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
)

const keyGomodWorker = "gomod-worker"

// Gomods outputs all the gomodule dependencies for the spec
func Gomods(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		sOpt, err := frontend.SourceOptFromClient(ctx, client, platform)
		if err != nil {
			return nil, nil, err
		}

		inputs, err := client.Inputs(ctx)
		if err != nil {
			return nil, nil, err
		}

		pg := dalec.ProgressGroup("gomod-deps")

		// Allow the client to override the worker image
		// This is useful for keeping pre-built worker image, especially for CI.
		worker, ok := inputs[keyGomodWorker]
		if !ok {
			worker = llb.Image("alpine:latest", llb.Platform(ocispecs.Platform{Architecture: runtime.GOARCH, OS: "linux"}), llb.WithMetaResolver(client), pg).
				Run(llb.Shlex("apk add --no-cache go git ca-certificates patch openssh"), pg).Root()
		}
		worker = worker.With(addedHosts(client))

		st := spec.GomodDeps(sOpt, worker, dalec.Platform(platform), pg)

		def, err := st.Marshal(ctx)
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
		return ref, &dalec.DockerImageSpec{}, nil
	})
}

func addedHosts(client gwclient.Client) llb.StateOption {
	return func(s llb.State) llb.State {
		ret := s
		bopts := client.BuildOpts().Opts
		if v, ok := bopts["add-hosts"]; ok {
			pairs := strings.Split(v, ",")
			for _, pair := range pairs {
				key, val, _ := strings.Cut(pair, "=")
				ret = ret.AddExtraHost(key, net.ParseIP(val))
			}
		}

		return ret
	}
}
