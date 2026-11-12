package main

import (
	"github.com/project-dalec/dalec/internal/commands"
	"github.com/project-dalec/dalec/internal/layersplit"
	"github.com/project-dalec/dalec/internal/plugins"
)

func init() {
	commands.RegisterPlugin(layersplit.CmdName, plugins.CmdHandlerFunc(layersplit.Cmd))
}
