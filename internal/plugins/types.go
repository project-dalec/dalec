package plugins

import (
	"github.com/project-dalec/dalec/frontend"
)

const (
	// TypeRouteProvider is a plugin type for route providers.
	// The returned plugin must implement the RouteProvider interface.
	TypeRouteProvider = "route-provider"
)

// RouteProvider is implemented by plugins that supply flat routes for the Router.
type RouteProvider interface {
	Routes() []frontend.Route
}

// RouteProviderFunc is a convenience adapter for RouteProvider.
type RouteProviderFunc func() []frontend.Route

func (f RouteProviderFunc) Routes() []frontend.Route {
	return f()
}
