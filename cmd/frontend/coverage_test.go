package main

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	digest "github.com/opencontainers/go-digest"
	"github.com/project-dalec/dalec/internal/frontendcoverage"
)

func TestWrapWithCoverageAttachesResultMetadataOnSuccess(t *testing.T) {
	t.Cleanup(setFrontendCoverageCollectorForTest(func() (*frontendcoverage.Payload, error) {
		return &frontendcoverage.Payload{
			MetaGz:     []byte("meta-gz"),
			CountersGz: []byte("counters-gz"),
		}, nil
	}))

	res, err := wrapWithCoverage(func(context.Context, gwclient.Client) (*gwclient.Result, error) {
		return nil, nil
	})(context.Background(), &fakeGatewayClient{
		opts: map[string]string{frontendcoverage.OptKey: "1"},
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if res == nil {
		t.Fatal("expected result to be created when coverage is enabled")
	}

	payload, payloadErr := frontendcoverage.PayloadFromSolve(res, nil)
	if payloadErr != nil {
		t.Fatalf("expected nil payload error, got %v", payloadErr)
	}
	if payload == nil {
		t.Fatal("expected payload to be attached to result metadata")
	}
	if !bytes.Equal(payload.MetaGz, []byte("meta-gz")) {
		t.Fatalf("unexpected meta payload: %q", payload.MetaGz)
	}
	if !bytes.Equal(payload.CountersGz, []byte("counters-gz")) {
		t.Fatalf("unexpected counters payload: %q", payload.CountersGz)
	}
}

func TestWrapWithCoverageAttachesGRPCDetailOnError(t *testing.T) {
	t.Cleanup(setFrontendCoverageCollectorForTest(func() (*frontendcoverage.Payload, error) {
		return &frontendcoverage.Payload{
			MetaGz:     []byte("meta-gz"),
			CountersGz: []byte("counters-gz"),
		}, nil
	}))

	frontendErr := errors.New("frontend failed")
	res, err := wrapWithCoverage(func(context.Context, gwclient.Client) (*gwclient.Result, error) {
		return nil, frontendErr
	})(context.Background(), &fakeGatewayClient{
		opts: map[string]string{frontendcoverage.OptKey: "1"},
	})
	if res != nil {
		t.Fatal("expected nil result on error path")
	}
	if err == nil {
		t.Fatal("expected error from wrapped frontend")
	}
	if !errors.Is(err, frontendErr) {
		t.Fatalf("expected wrapped error to preserve original error, got %v", err)
	}
	if err.Error() != frontendErr.Error() {
		t.Fatalf("expected wrapped error message %q, got %q", frontendErr.Error(), err.Error())
	}

	payload, payloadErr := frontendcoverage.PayloadFromError(err)
	if payloadErr != nil {
		t.Fatalf("expected nil payload error, got %v", payloadErr)
	}
	if payload == nil {
		t.Fatal("expected payload to be attached to error details")
	}
	if !bytes.Equal(payload.MetaGz, []byte("meta-gz")) {
		t.Fatalf("unexpected meta payload: %q", payload.MetaGz)
	}
	if !bytes.Equal(payload.CountersGz, []byte("counters-gz")) {
		t.Fatalf("unexpected counters payload: %q", payload.CountersGz)
	}
}

func setFrontendCoverageCollectorForTest(f func() (*frontendcoverage.Payload, error)) func() {
	previous := frontendCoverageCollector
	frontendCoverageCollector = f
	return func() {
		frontendCoverageCollector = previous
	}
}

type fakeGatewayClient struct {
	opts map[string]string
}

func (c *fakeGatewayClient) Solve(context.Context, gwclient.SolveRequest) (*gwclient.Result, error) {
	panic("unexpected call to Solve")
}

func (c *fakeGatewayClient) ResolveImageConfig(context.Context, string, sourceresolver.Opt) (string, digest.Digest, []byte, error) {
	panic("unexpected call to ResolveImageConfig")
}

func (c *fakeGatewayClient) ResolveSourceMetadata(context.Context, *pb.SourceOp, sourceresolver.Opt) (*sourceresolver.MetaResponse, error) {
	panic("unexpected call to ResolveSourceMetadata")
}

func (c *fakeGatewayClient) BuildOpts() gwclient.BuildOpts {
	return gwclient.BuildOpts{Opts: c.opts}
}

func (c *fakeGatewayClient) Inputs(context.Context) (map[string]llb.State, error) {
	panic("unexpected call to Inputs")
}

func (c *fakeGatewayClient) NewContainer(context.Context, gwclient.NewContainerRequest) (gwclient.Container, error) {
	panic("unexpected call to NewContainer")
}

func (c *fakeGatewayClient) Warn(context.Context, digest.Digest, string, gwclient.WarnOpts) error {
	panic("unexpected call to Warn")
}
