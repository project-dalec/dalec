package frontend

import (
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

// currentFrontend is an interface typically implemented by a [gwclient.Client].
// This is used to get the rootfs of the current frontend.
type currentFrontend interface {
	CurrentFrontend() (*llb.State, error)
}

// withCurrentFrontend wraps a [gwclient.Client] to ensure that [currentFrontend]
// is not lost when the client is wrapped.
//
// If inner does not implement [currentFrontend], wrapper is returned as-is.
func withCurrentFrontend(inner gwclient.Client, wrapper gwclient.Client) gwclient.Client {
	cf, ok := inner.(currentFrontend)
	if !ok {
		return wrapper
	}

	return &clientWithCurrentFrontend{Client: wrapper, cf: cf}
}

// clientWithCurrentFrontend wraps a gwclient.Client and forwards
// CurrentFrontend calls to the inner client.
type clientWithCurrentFrontend struct {
	gwclient.Client
	cf currentFrontend
}

var _ gwclient.Client = (*clientWithCurrentFrontend)(nil)

func (c *clientWithCurrentFrontend) CurrentFrontend() (*llb.State, error) {
	return c.cf.CurrentFrontend()
}
