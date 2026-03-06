package frontendapi

import (
	"context"
	"fmt"
	"sync"

	"github.com/containerd/plugin"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
	"github.com/project-dalec/dalec/frontend/debug"
	"github.com/project-dalec/dalec/internal/gwutil"
	"github.com/project-dalec/dalec/internal/plugins"
	_ "github.com/project-dalec/dalec/targets/plugin"
)

// NewRouter creates a flat Router with all routes registered eagerly.
func NewRouter(ctx context.Context, client gwclient.Client) (*frontend.Router, error) {
	client = newCachedSpecClient(client)
	r := &frontend.Router{}

	// Register debug routes.
	for _, route := range debug.Routes(debug.DebugRoute) {
		r.Add(ctx, route)
	}

	// Load route providers from the plugin registry.
	if err := loadRouteProviders(ctx, client, r); err != nil {
		return nil, err
	}

	return r, nil
}

func loadRouteProviders(ctx context.Context, client gwclient.Client, r *frontend.Router) error {
	set := plugin.NewPluginSet()

	filter := func(reg *plugins.Registration) bool {
		return reg.Type != plugins.TypeRouteProvider
	}

	for _, reg := range plugins.Graph(filter) {
		cfg := plugin.NewContext(ctx, set, nil)

		p := reg.Init(cfg)
		if err := set.Add(p); err != nil {
			return err
		}

		v, err := p.Instance()
		if err != nil {
			if plugin.IsSkipPlugin(err) {
				continue
			}
			return err
		}

		provider, ok := v.(plugins.RouteProvider)
		if !ok {
			return fmt.Errorf("plugin %T does not implement RouteProvider", v)
		}
		routes, err := provider.Routes(ctx, client)
		if err != nil {
			return err
		}
		for _, route := range routes {
			r.Add(ctx, route)
		}
	}

	return nil
}

// cachedSpecClient wraps a gwclient.Client and caches the spec loaded by
// LoadSpecFromClient so that it is only loaded once.
type cachedSpecClient struct {
	gwclient.Client

	loadOnce sync.Once
	spec     *dalec.Spec
	err      error
}

// Compile-time check that cachedSpecClient implements gwutil.SpecLoader.
var _ gwutil.SpecLoader = (*cachedSpecClient)(nil)

func newCachedSpecClient(client gwclient.Client) gwclient.Client {
	return gwutil.WithCurrentFrontend(client, &cachedSpecClient{
		Client: client,
	})
}

func (c *cachedSpecClient) LoadSpec(ctx context.Context) (*dalec.Spec, error) {
	c.loadOnce.Do(func() {
		c.spec, c.err = frontend.LoadSpecFromClient(ctx, c.Client)
	})
	return c.spec, c.err
}
