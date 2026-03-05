package plugins

import (
	"context"

	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
)

const (
	// TypeRouteProvider is a plugin type for route providers.
	// The returned plugin must implement the RouteProvider interface.
	TypeRouteProvider = "route-provider"
)

// RouteProvider is implemented by plugins that supply flat routes for the Router.
type RouteProvider interface {
	Routes(ctx context.Context, spec *dalec.Spec) ([]frontend.Route, error)
}

// RouteProviderFunc is a convenience adapter for RouteProvider.
type RouteProviderFunc func(ctx context.Context, spec *dalec.Spec) ([]frontend.Route, error)

func (f RouteProviderFunc) Routes(ctx context.Context, spec *dalec.Spec) ([]frontend.Route, error) {
	return f(ctx, spec)
}
