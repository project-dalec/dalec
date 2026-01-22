package build

import (
	"context"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

var (
	goModCache   = llb.AddMount("/go/pkg/mod", llb.Scratch(), llb.AsPersistentCacheDir("/go/pkg/mod", llb.CacheMountShared))
	goBuildCache = llb.AddMount("/root/.cache/go-build", llb.Scratch(), llb.AsPersistentCacheDir("/root/.cache/go-build", llb.CacheMountShared))
)

func HTTPGitServer(ctx context.Context, gwc gwclient.Client) (*llb.State, error) {
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
			llb.Args([]string{"go", "build", "-o=/build/out/git_http_server", "./test/cmd/git_repo"}),
			llb.AddEnv("CGO_ENABLED", "0"),
			goModCache,
			goBuildCache,
			llb.Dir("/build/src"),
			llb.AddMount("/build/src", *bctx, llb.Readonly),
		).
		AddMount("/build/out", llb.Scratch())

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

	ref, err := res.SingleRef()
	if err != nil {
		return nil, err
	}

	built, err := ref.ToState()
	if err != nil {
		return nil, err
	}

	return &built, nil
}
