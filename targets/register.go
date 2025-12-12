package targets

import (
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/project-dalec/dalec/internal/plugins"
)

func RegisterBuildTarget(name string, build gwclient.BuildFunc) {
	plugins.Register(&plugins.Registration{
		ID:   name,
		Type: plugins.TypeBuildTarget,
		InitFn: func(*plugins.InitContext) (any, error) {
			return plugins.BuildHandlerFunc(build), nil
		},
	})
}
