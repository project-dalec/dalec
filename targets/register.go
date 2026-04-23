package targets

import (
	"context"

	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
	"github.com/project-dalec/dalec/internal/plugins"
)

// RegisterRouteProvider registers a plugin that provides flat routes for the Router.
func RegisterRouteProvider(name string, routes func(ctx context.Context, spec *dalec.Spec) ([]frontend.Route, error)) {
	plugins.Register(&plugins.Registration{
		ID:   name,
		Type: plugins.TypeRouteProvider,
		InitFn: func(*plugins.InitContext) (interface{}, error) {
			return plugins.RouteProviderFunc(routes), nil
		},
	})
}
