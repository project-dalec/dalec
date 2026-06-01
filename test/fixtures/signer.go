package fixtures

import (
	"context"
	"encoding/json"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/project-dalec/dalec"
)

func PhonySigner(ctx context.Context, gwc gwclient.Client) (*gwclient.Result, error) {
	dc, err := dockerui.NewClient(gwc)
	if err != nil {
		return nil, err
	}

	bctx, err := dc.MainContext(ctx)
	if err != nil {
		return nil, err
	}

	p := llb.Platform(dc.BuildPlatforms[0])
	st := llb.Image("golang:1.25", llb.WithMetaResolver(gwc), p).
		Run(
			llb.Args([]string{"go", "build", "-o=/build/out", "./test/fixtures/signer"}),
			llb.AddEnv("CGO_ENABLED", "0"),
			goModCache,
			goBuildCache,
			llb.Dir("/build/src"),
			llb.AddMount("/build/src", *bctx, llb.Readonly),
		).
		AddMount("/build/out", llb.Scratch())

	cfg := dalec.DockerImageSpec{
		Config: dalec.DockerImageConfig{
			ImageConfig: ocispecs.ImageConfig{
				Entrypoint: []string{"/signer"},
			},
		},
	}
	injectFrontendLabels(&cfg)

	dt, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}

	def, err := st.Marshal(ctx)
	if err != nil {
		return nil, err
	}

	res, err := gwc.Solve(ctx, gwclient.SolveRequest{
		Definition: def.ToPB(),
	})
	if err != nil {
		return nil, err
	}

	res.AddMeta(exptypes.ExporterImageConfigKey, dt)

	return res, nil
}
