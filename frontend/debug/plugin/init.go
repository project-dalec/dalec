package plugin

import (
	"github.com/project-dalec/dalec/frontend/debug"
	"github.com/project-dalec/dalec/internal/plugins"
)

func init() {
	plugins.Register(&plugins.Registration{
		Type: plugins.TypeBuildTarget,
		ID:   debug.DebugRoute,
		InitFn: func(_ *plugins.InitContext) (interface{}, error) {
			return plugins.BuildHandlerFunc(debug.Handle), nil
		},
	})
}
