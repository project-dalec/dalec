package targets

import (
	"context"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/project-dalec/dalec/frontend"
	"github.com/project-dalec/dalec/internal/plugins"
)

// RegisterRouteProvider registers a plugin that provides flat routes for the Router.
func RegisterRouteProvider(name string, routes func(ctx context.Context, client gwclient.Client) ([]frontend.Route, error)) {
	plugins.Register(&plugins.Registration{
		ID:   name,
		Type: plugins.TypeRouteProvider,
		InitFn: func(*plugins.InitContext) (interface{}, error) {
			return plugins.RouteProviderFunc(routes), nil
		},
	})
}
