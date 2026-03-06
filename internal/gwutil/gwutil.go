package gwutil

import (
	"context"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/project-dalec/dalec"
)

// CurrentFrontend is an interface typically implemented by a [gwclient.Client].
// This is used to get the rootfs of the current frontend.
type CurrentFrontend interface {
	CurrentFrontend() (*llb.State, error)
}

// SpecLoader is an optional interface that a [gwclient.Client] wrapper can
// implement to provide a cached or pre-loaded dalec spec. This avoids
// redundant loads when many callers need the spec from the same client.
type SpecLoader interface {
	LoadSpec(context.Context) (*dalec.Spec, error)
}

// WithCurrentFrontend wraps a [gwclient.Client] to ensure that [CurrentFrontend]
// is not lost when the client is wrapped.
//
// If inner does not implement [CurrentFrontend], wrapper is returned as-is.
// Otherwise, if either the wrapper or the inner client implements [SpecLoader],
// that interface is also preserved on the returned client (wrapper is preferred
// over inner).
func WithCurrentFrontend(inner gwclient.Client, wrapper gwclient.Client) gwclient.Client {
	cf, ok := inner.(CurrentFrontend)
	if !ok {
		return wrapper
	}

	w := &clientWithCurrentFrontend{Client: wrapper, cf: cf}

	// Preserve SpecLoader if present on either wrapper or inner.
	// Prefer wrapper (it's the more specific layer).
	switch {
	case isSpecLoader(wrapper):
		return &clientWithCurrentFrontendAndSpecLoader{clientWithCurrentFrontend: w, sl: wrapper.(SpecLoader)}
	case isSpecLoader(inner):
		return &clientWithCurrentFrontendAndSpecLoader{clientWithCurrentFrontend: w, sl: inner.(SpecLoader)}
	}

	return w
}

func isSpecLoader(c gwclient.Client) bool {
	_, ok := c.(SpecLoader)
	return ok
}

// clientWithCurrentFrontend wraps a gwclient.Client and forwards
// CurrentFrontend calls to the inner client.
type clientWithCurrentFrontend struct {
	gwclient.Client
	cf CurrentFrontend
}

var _ gwclient.Client = (*clientWithCurrentFrontend)(nil)

func (c *clientWithCurrentFrontend) CurrentFrontend() (*llb.State, error) {
	return c.cf.CurrentFrontend()
}

// clientWithCurrentFrontendAndSpecLoader extends clientWithCurrentFrontend
// with SpecLoader forwarding.
type clientWithCurrentFrontendAndSpecLoader struct {
	*clientWithCurrentFrontend
	sl SpecLoader
}

var (
	_ gwclient.Client = (*clientWithCurrentFrontendAndSpecLoader)(nil)
	_ SpecLoader      = (*clientWithCurrentFrontendAndSpecLoader)(nil)
)

func (c *clientWithCurrentFrontendAndSpecLoader) LoadSpec(ctx context.Context) (*dalec.Spec, error) {
	return c.sl.LoadSpec(ctx)
}
