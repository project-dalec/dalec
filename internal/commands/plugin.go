package commands

import "github.com/project-dalec/dalec/internal/plugins"

func RegisterPlugin(name string, h plugins.CmdHandler) {
	plugins.Register(&plugins.Registration{
		ID:   name,
		Type: plugins.TypeCmd,
		InitFn: func(*plugins.InitContext) (any, error) {
			return h, nil
		},
	})
}
