package plugins

import (
	"context"

	"github.com/project-dalec/dalec/frontend"
)

const (
	TypeCmd = "cmd"
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

type CmdHandler interface {
	HandleCmd(ctx context.Context, args []string)
}

type CmdHandlerFunc func(ctx context.Context, args []string)

func (f CmdHandlerFunc) HandleCmd(ctx context.Context, args []string) {
	f(ctx, args)
}
