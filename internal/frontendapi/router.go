package frontendapi

import (
	"context"
	"fmt"

	"github.com/containerd/plugin"
	"github.com/project-dalec/dalec/frontend"
	"github.com/project-dalec/dalec/frontend/debug"
	"github.com/project-dalec/dalec/internal/plugins"
	_ "github.com/project-dalec/dalec/targets/plugin"
)

// NewRouter creates a Router with all routes registered.
func NewRouter(ctx context.Context) (*frontend.Router, error) {
	r := &frontend.Router{}

	// Register debug routes.
	for _, route := range debug.Routes(debug.DebugRoute) {
		r.Add(ctx, route)
	}

	// Load route providers from the plugin registry.
	if err := loadRouteProviders(ctx, r); err != nil {
		return nil, err
	}

	return r, nil
}

func loadRouteProviders(ctx context.Context, r *frontend.Router) error {
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
		for _, route := range provider.Routes() {
			r.Add(ctx, route)
		}
	}

	return nil
}
