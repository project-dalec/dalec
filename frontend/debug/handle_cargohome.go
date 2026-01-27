package debug

import (
	"context"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
)

const keyCargohomeWorker = "context:cargohome-worker"

// Cargohome outputs all the Cargo dependencies for the spec
func Cargohome(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		sOpt, err := frontend.SourceOptFromClient(ctx, client, platform)
		if err != nil {
			return nil, nil, err
		}

		inputs, err := client.Inputs(ctx)
		if err != nil {
			return nil, nil, err
		}

		pg := dalec.ProgressGroup("cargohome-deps")

		// Allow the client to override the worker image
		// This is useful for keeping pre-built worker images, especially for CI.
		worker, ok := inputs[keyCargohomeWorker]
		if !ok {
			worker = llb.Image("rust:latest", llb.WithMetaResolver(client), pg).
				Run(llb.Shlex("cargo --version"), pg).Root()
		}

		st := spec.CargohomeDeps(sOpt, worker, dalec.Platform(platform), pg)

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
