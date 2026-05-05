package plugins

import (
	"context"

	"github.com/moby/buildkit/frontend/gateway/client"
)

const (
	// TypeBuildTarget is a plugin type for build targets.
	// The returned plugin must implement the BuildHandler interface
	TypeBuildTarget = "build-target"
	TypeCmd         = "cmd"
)

type BuildHandler interface {
	HandleBuild(ctx context.Context, client client.Client) (*client.Result, error)
}

type BuildHandlerFunc func(ctx context.Context, client client.Client) (*client.Result, error)

func (f BuildHandlerFunc) HandleBuild(ctx context.Context, client client.Client) (*client.Result, error) {
	return f(ctx, client)
}

type CmdHandler interface {
	HandleCmd(ctx context.Context, args []string)
}

type CmdHandlerFunc func(ctx context.Context, args []string)

func (f CmdHandlerFunc) HandleCmd(ctx context.Context, args []string) {
	f(ctx, args)
}
