package frontend

import (
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	"github.com/project-dalec/dalec"
)

// NativeSymlinkSupport reports whether the given client's buildkit supports the
// native LLB symlink file action.
func NativeSymlinkSupport(client gwclient.Client) dalec.NativeSymlinkSupport {
	return &nativeSymlinkSupport{client}
}

type nativeSymlinkSupport struct {
	client gwclient.Client
}

func (c *nativeSymlinkSupport) SupportsNativeSymlinks() bool {
	opts := c.client.BuildOpts()
	if opts.Opts["build-arg:DALEC_DISABLE_SYMLINK"] == "1" {
		return false
	}
	return opts.LLBCaps.Supports(pb.CapFileSymlinkCreate) == nil
}
