package targets

import (
	"github.com/project-dalec/dalec/internal/plugins"
)

func RegisterBuildTarget(name string, build plugins.BuildHandler) {
	plugins.Register(&plugins.Registration{
		ID:   name,
		Type: plugins.TypeBuildTarget,
		InitFn: func(*plugins.InitContext) (interface{}, error) {
			return build, nil
		},
	})
}
