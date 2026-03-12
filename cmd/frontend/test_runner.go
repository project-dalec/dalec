package main

import (
	"github.com/project-dalec/dalec/internal/commands"
	"github.com/project-dalec/dalec/internal/plugins"
	"github.com/project-dalec/dalec/internal/testrunner"
)

func init() {
	var runner testrunner.Runner
	commands.RegisterPlugin(testrunner.CmdName, plugins.CmdHandlerFunc(runner.Cmd))
}
