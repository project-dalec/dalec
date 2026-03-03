package frontend

import (
	"context"
	"errors"
	"maps"
	"testing"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	bktargets "github.com/moby/buildkit/frontend/subrequests/targets"
	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
)

func TestRouter(t *testing.T) {
	ctx := context.Background()

	var r Router

	newCallback := func() (count func() int, bf gwclient.BuildFunc) {
		var i int

		count = func() int {
			return i
		}
		bf = stubHandler(func() {
			i++
		})
		return count, bf
	}

	// Register a route at a fully qualified path.
	realCount, realH := newCallback()
	r.Add(ctx, Route{
		FullPath: "real",
		Handler:  realH,
		Info: Target{
			Target: bktargets.Target{
				Name:    "real",
				Default: true,
			},
		},
	})

	expectedRealCount := 0
	client := newStubClient(withStubOptTarget("real"))
	_, err := r.Handle(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	expectedRealCount++

	if count := realCount(); count != expectedRealCount {
		t.Errorf("expected real handler call count to be %d, got %d", expectedRealCount, count)
	}

	// Register a sub-route at a fully qualified path.
	subRouteACount, subRouteAH := newCallback()
	r.Add(ctx, Route{
		FullPath: "real/subroute/a",
		Handler:  subRouteAH,
		Info: Target{
			Target: bktargets.Target{Name: "real/subroute/a"},
		},
	})
	expectedSubRouteACount := 0

	// Same target again — should still call the exact-match "real" handler.
	_, err = r.Handle(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	expectedRealCount++
	if count := realCount(); count != expectedRealCount {
		t.Fatalf("expected real handler to be called %d times, got %d", expectedRealCount, count)
	}
	if count := subRouteACount(); count != expectedSubRouteACount {
		t.Errorf("expected real/subroute/a handler to be called %d times, got %d", expectedSubRouteACount, count)
	}

	// Now target the sub-route exactly.
	client = newStubClient(withStubOptTarget("real/subroute/a"))
	_, err = r.Handle(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	expectedSubRouteACount++

	if count := realCount(); count != expectedRealCount {
		t.Errorf("expected real handler to still be called %d times, got %d", expectedRealCount, count)
	}
	if count := subRouteACount(); count != expectedSubRouteACount {
		t.Errorf("expected real/subroute/a handler to be called %d times, got %d", expectedSubRouteACount, count)
	}
}

func TestRouterPrefixMatch(t *testing.T) {
	ctx := context.Background()
	var r Router

	called := false
	r.Add(ctx, Route{
		FullPath: "azlinux3/container",
		Handler: func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
			called = true
			return nil, nil
		},
		Info: Target{
			Target: bktargets.Target{Name: "azlinux3/container"},
		},
	})

	// Target includes a suffix beyond the registered route.
	client := newStubClient(withStubOptTarget("azlinux3/container/with-contrib"))
	_, err := r.Handle(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("expected prefix-matched handler to be called")
	}
}

func TestRouterEmptyTargetReturnsError(t *testing.T) {
	ctx := context.Background()
	var r Router

	r.Add(ctx, Route{
		FullPath: "mydefault",
		Handler:  stubHandler(func() {}),
		Info: Target{
			Target: bktargets.Target{Name: "mydefault", Default: true},
		},
	})

	// Empty target should return an error prompting the user to specify a target.
	client := newStubClient(withStubOptTarget(""))
	_, err := r.Handle(ctx, client)
	if err == nil {
		t.Fatal("expected error for empty target")
	}
	var nsh *noSuchHandlerError
	if !errors.As(err, &nsh) {
		t.Fatalf("expected noSuchHandlerError, got %T: %v", err, err)
	}
}

func TestRouterNotFound(t *testing.T) {
	ctx := context.Background()
	var r Router

	r.Add(ctx, Route{
		FullPath: "foo",
		Handler:  stubHandler(func() {}),
		Info:     Target{Target: bktargets.Target{Name: "foo"}},
	})

	client := newStubClient(withStubOptTarget("bar"))
	_, err := r.Handle(ctx, client)
	if err == nil {
		t.Fatal("expected error for unknown target")
	}
	var nsh *noSuchHandlerError
	if !errors.As(err, &nsh) {
		t.Fatalf("expected noSuchHandlerError, got %T: %v", err, err)
	}
}

func stubHandler(cb func()) gwclient.BuildFunc {
	return func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
		cb()
		return nil, nil
	}
}

var _ gwclient.Client = (*stubClient)(nil)

type stubClient struct {
	opts     map[string]string
	inputs   map[string]llb.State
	imageRes llb.ImageMetaResolver
	metaRes  sourceresolver.MetaResolver
}

type stubOpt func(*stubClient)

func withStubOptTarget(t string) stubOpt {
	return func(c *stubClient) {
		c.opts[keyTarget] = t
	}
}

func newStubClient(opts ...stubOpt) *stubClient {
	c := &stubClient{
		opts:   make(map[string]string),
		inputs: make(map[string]llb.State),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func (c *stubClient) BuildOpts() gwclient.BuildOpts {
	return gwclient.BuildOpts{
		Opts:    maps.Clone(c.opts),
		LLBCaps: pb.Caps.CapSet(pb.Caps.All()),
		Caps:    pb.Caps.CapSet(pb.Caps.All()),
	}
}

func (c *stubClient) Inputs(context.Context) (map[string]llb.State, error) {
	return maps.Clone(c.inputs), nil
}

func (c *stubClient) NewContainer(context.Context, gwclient.NewContainerRequest) (gwclient.Container, error) {
	return nil, errors.New("not implemented")
}

func (c *stubClient) ResolveImageConfig(ctx context.Context, ref string, opt sourceresolver.Opt) (string, digest.Digest, []byte, error) {
	if c.imageRes == nil {
		return "", "", nil, errors.New("not implemented")
	}
	return c.imageRes.ResolveImageConfig(ctx, ref, opt)
}

func (c *stubClient) ResolveSourceMetadata(ctx context.Context, op *pb.SourceOp, opt sourceresolver.Opt) (*sourceresolver.MetaResponse, error) {
	if c.metaRes == nil {
		return nil, errors.New("not implemented")
	}
	return c.metaRes.ResolveSourceMetadata(ctx, op, opt)
}

func (c *stubClient) Solve(ctx context.Context, req gwclient.SolveRequest) (*gwclient.Result, error) {
	return nil, errors.New("not implemented")
}

func (c *stubClient) Warn(ctx context.Context, dgst digest.Digest, msg string, opts gwclient.WarnOpts) error {
	return errors.New("not implemented")
}
