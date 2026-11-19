package frontend

import (
	"net"
	"strings"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

// AddedHosts returns a state option that applies the client's add-hosts
// frontend option to containers created from the state.
func AddedHosts(client gwclient.Client) llb.StateOption {
	return func(s llb.State) llb.State {
		for _, pair := range strings.Split(client.BuildOpts().Opts["add-hosts"], ",") {
			if pair == "" {
				continue
			}
			key, val, ok := strings.Cut(pair, "=")
			if !ok || key == "" {
				continue
			}
			ip := net.ParseIP(val)
			if ip == nil {
				continue
			}
			s = s.AddExtraHost(key, ip)
		}
		return s
	}
}
