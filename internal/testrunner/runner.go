package testrunner

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/internal/commands"
	"github.com/project-dalec/dalec/internal/plugins"
)

func init() {
	var runner Runner
	commands.RegisterPlugin(testRunnerCmdName, plugins.CmdHandlerFunc(runner.Cmd))
}

const testRunnerCmdName = "test-runner"

type Runner struct{}

func (tr *Runner) Cmd(ctx context.Context, args []string) {
	flags := flag.NewFlagSet(testRunnerCmdName, flag.ExitOnError)
	flags.Parse(args) //nolint:errcheck // errors are handled by ExitOnError

	if flags.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "no test-runner command provided")
		os.Exit(1)
	}

	cmd := flags.Arg(0)
	args = flags.Args()[1:]
	switch cmd {
	case string(checkFilePerms):
		checkFilePerms.Cmd(args)
	case string(checkFileExists):
		checkFileExists.Cmd(args)
	case string(checkFileContains):
		checkFileContains.Cmd(args)
	case string(checkFileEmpty):
		checkFileEmpty.Cmd(args)
	case string(checkFileStartsWith):
		checkFileStartsWith.Cmd(args)
	case string(checkFileEndsWith):
		checkFileEndsWith.Cmd(args)
	case string(checkFileMatches):
		checkFileMatches.Cmd(args)
	case string(checkFileEquals):
		checkFileEquals.Cmd(args)
	case string(checkFileIsDir):
		checkFileIsDir.Cmd(args)
	case string(stepRunner):
		stepRunner.Cmd(ctx, args)
	case string(trueCmd):
		trueCmd.Cmd(args)
	default:
		fmt.Fprintln(os.Stderr, testRunnerCmdName+":", "Unknown command:", cmd)
		os.Exit(70) // 70 is EX_SOFTWARE, meaning internal software error occurred
	}
}

func doValidate(args []string, opts ...ValidationOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		const (
			internalStatePath = "/tmp/internal/dalec/testrunner/__internal_state"
		)

		return in.Run(
			testRunner(args, opts...),
		).AddMount(internalStatePath, in)
	}
}

func testRunner(args []string, opts ...ValidationOpt) llb.RunOption {
	return dalec.RunOptFunc(func(ei *llb.ExecInfo) {
		const p = "/tmp/internal/dalec/testrunner/frontend"

		args = append([]string{p, testRunnerCmdName}, args...)
		llb.Args(args).SetRunOption(ei)

		info := validationOpts(opts...)
		llb.AddMount(p, *info.Frontend, llb.Readonly, llb.SourcePath("/frontend")).SetRunOption(ei)

		for _, o := range info.Constraints {
			o.SetRunOption(ei)
		}
	})
}

type ValidationOpt func(*ValidationInfo)

type ValidationInfo struct {
	Frontend    *llb.State
	Constraints []llb.ConstraintsOpt
}

func (i *ValidationInfo) SetConstraintsOption(c *llb.Constraints) {
	for _, opt := range i.Constraints {
		opt.SetConstraintsOption(c)
	}
}

func WithConstraints(opts ...llb.ConstraintsOpt) ValidationOpt {
	return func(vi *ValidationInfo) {
		vi.Constraints = append(vi.Constraints, opts...)
	}
}

func validationOpts(opts ...ValidationOpt) ValidationInfo {
	var i ValidationInfo
	for _, o := range opts {
		o(&i)
	}

	if i.Frontend == nil {
		panic("missing frontend state in validation opt")
	}

	return i
}

func asConstraints(opts ...ValidationOpt) llb.ConstraintsOpt {
	return dalec.ConstraintsOptFunc(func(c *llb.Constraints) {
		vi := validationOpts(opts...)
		vi.SetConstraintsOption(c)
	})
}

// For each llb.StateOption, set the state option on the original state
// Example:
// origState:
// |-apply(opt) -> state1
// |-apply(opt) -> state2
// |-apply(opt) -> state3
//
// In the above example, each of the new states (state1,state2,state3) are direct decendants
// of origState.
// If you are expecting states to apply in order (origState -> state1 -> state2 -> state3),
// this is not the function you are looking for.
func mergeStateOptions(stateOpts []llb.StateOption, opts ...ValidationOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		if len(opts) == 0 {
			return in
		}

		states := make([]llb.State, 0, len(opts))
		for _, o := range stateOpts {
			states = append(states, in.With(o))
		}
		return dalec.MergeAtPath(in, states, "/", asConstraints(opts...))
	}
}

func filterPath(p string) string {
	if strings.HasPrefix(p, "/tmp/internal/dalec") {
		return filepath.Base(p)
	}
	return p
}
