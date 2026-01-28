package plugins

import (
	"context"

	"github.com/moby/buildkit/frontend/gateway/client"
)

const (
	// TypeBuildTarget is a plugin type for build targets.
	// The returned plugin must implement the BuildHandler interface
	TypeBuildTarget = "build-target"
)

type BuildHandler interface {
	Handle(ctx context.Context, client client.Client) (*client.Result, error)
}

type BuildHandlerFunc func(ctx context.Context, client client.Client) (*client.Result, error)

func (f BuildHandlerFunc) Handle(ctx context.Context, client client.Client) (*client.Result, error) {
	return f(ctx, client)
}
