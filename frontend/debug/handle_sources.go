package debug

import (
	"context"

	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

// Sources is a handler that outputs all the sources.
func Sources(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		sOpt, err := frontend.SourceOptFromClient(ctx, client, platform)
		if err != nil {
			return nil, nil, err
		}

		sources, err := dalec.Sources(spec, sOpt)
		if err != nil {
			return nil, nil, err
		}

		def, err := dalec.MergeAtPath(llb.Scratch(), dalec.SortedMapValues(sources), "/").Marshal(ctx)
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

// Sources is a handler that outputs all the sources.
func PatchedSources(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		const keyPatchedSourcesWorker = "context:patched-sources-worker"
		sOpt, err := frontend.SourceOptFromClient(ctx, client, platform)
		if err != nil {
			return nil, nil, err
		}

		inputs, err := client.Inputs(ctx)
		if err != nil {
			return nil, nil, err
		}

		worker, ok := inputs[keyPatchedSourcesWorker]
		if !ok {
			worker = llb.Image("alpine:latest", llb.WithMetaResolver(client)).
				Run(llb.Shlex("apk add --no-cache go git ca-certificates patch")).Root()
		}

		pc := dalec.Platform(platform)
		if err := spec.EnsureGomodPatches(sOpt, worker, pc); err != nil {
			return nil, nil, err
		}
		sources, err := dalec.Sources(spec, sOpt, pc)
		if err != nil {
			return nil, nil, err
		}

		sources = dalec.PatchSources(worker, spec, sources, pc)

		def, err := dalec.MergeAtPath(llb.Scratch(), dalec.SortedMapValues(sources), "/").Marshal(ctx)
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
