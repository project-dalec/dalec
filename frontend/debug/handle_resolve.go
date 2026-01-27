package debug

import (
	"context"
	"fmt"

	yaml "github.com/goccy/go-yaml"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
)

// Resolve is a handler that generates a resolved spec file with all the build args and variables expanded.
func Resolve(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		dt, err := yaml.Marshal(spec)
		if err != nil {
			return nil, nil, fmt.Errorf("error marshalling spec: %w", err)
		}
		st := llb.Scratch().File(llb.Mkfile("spec.yml", 0640, dt), llb.WithCustomName("Generate resolved spec file - spec.yml"))
		def, err := st.Marshal(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("error marshalling llb: %w", err)
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

		return ref, &dalec.DockerImageSpec{}, err
	})
}
