package testrunner

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
)

const CmdName = "test-runner"

type Runner struct{}

func (tr *Runner) Cmd(ctx context.Context, args []string) {
	flags := flag.NewFlagSet(CmdName, flag.ExitOnError)
	flags.Parse(args) //nolint:errcheck // errors are handled by ExitOnError

	if flags.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "no test-runner command provided")
		exit(1)
	}

	cmd := flags.Arg(0)
	args = flags.Args()[1:]
	switch cmd {
	case string(checkFilePerms):
		checkFilePerms.Cmd(args)
	case string(checkFileNotExists):
		checkFileNotExists.Cmd(args)
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
	case string(checkFileLinkTarget):
		checkFileLinkTarget.Cmd(args)
	case string(stepRunner):
		stepRunner.Cmd(ctx, args)
	case string(trueCmd):
		trueCmd.Cmd(args)
	default:
		fmt.Fprintln(os.Stderr, CmdName+":", "Unknown command:", cmd)
		exit(70) // 70 is EX_SOFTWARE, meaning internal software error occurred
	}
}

func doValidate(args []string, opts ...ValidationOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		// Run the validation as a real (writable-rootfs) exec so the vertex is
		// not optimized away, then require it to build while keeping `in` as the
		// output via buildkit's passthrough op. The validation's filesystem
		// changes are discarded; only the dependency edge (and its pass/fail) is
		// kept.
		runState := in.Run(
			testRunner(args, opts...),
			llb.WithCustomName(strings.Join(args, " ")),
		).Root()

		return in.Requires("dalec.testrunner/validate", runState)
	}
}

func testRunner(args []string, opts ...ValidationOpt) llb.RunOption {
	return dalec.RunOptFunc(func(ei *llb.ExecInfo) {
		const p = "/tmp/internal/dalec/testrunner/frontend"

		args = append([]string{p, CmdName}, args...)
		llb.Args(args).SetRunOption(ei)

		info := validationOpts(opts...)
		llb.AddMount(p, *info.Frontend, llb.Readonly, llb.SourcePath("/frontend")).SetRunOption(ei)
		info.SetRunOption(ei)
	})
}

type ValidationOpt func(*ValidationInfo)

type ValidationInfo struct {
	Frontend    *llb.State
	Constraints []llb.ConstraintsOpt
	ExtraOpts   []llb.RunOption
}

func validationOptsFromTest(t *dalec.TestSpec, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) ValidationOpt {
	return func(i *ValidationInfo) {
		for k, v := range t.Env {
			i.ExtraOpts = append(i.ExtraOpts, llb.AddEnv(k, v))
		}

		for _, mnt := range t.Mounts {
			i.ExtraOpts = append(i.ExtraOpts, mnt.ToRunOption(sOpt, dalec.WithConstraints(opts...)))
		}
	}
}

func ValidateWithMount(sOpt dalec.SourceOpts, mnt dalec.SourceMount, opts ...llb.ConstraintsOpt) ValidationOpt {
	return func(i *ValidationInfo) {
		i.ExtraOpts = append(i.ExtraOpts, mnt.ToRunOption(sOpt, dalec.WithConstraints(opts...)))
	}
}

func (i *ValidationInfo) SetRunOption(ei *llb.ExecInfo) {
	c := ei.Constraints
	i.SetConstraintsOption(&c)
	ei.Constraints = c

	for _, opt := range i.ExtraOpts {
		opt.SetRunOption(ei)
	}
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

// mergeStateOptions applies multiple llb.StateOptions to the same input state,
// forcing each to build while preserving `in` as the output.
//
// Each option is applied independently to `in` (no option sees the effects of
// another), and the resulting states are attached as build-only dependencies of
// `in` via buildkit's passthrough op. The filesystem changes from each option
// are discarded; only the dependency edges (and their pass/fail) are kept.
//
// This relies on all testrunner options being validation-only (they return the
// input state unchanged); it does not merge filesystem changes.
func mergeStateOptions(stateOpts []llb.StateOption, _ ...ValidationOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		deps := make([]llb.State, 0, len(stateOpts))
		for _, o := range stateOpts {
			deps = append(deps, in.With(o))
		}

		return in.Requires("dalec.testrunner/checks", deps...)
	}
}

func filterPath(p string) string {
	if strings.HasPrefix(p, "/tmp/internal/dalec") {
		return filepath.Base(p)
	}
	return p
}

// WithFinalState returns a state option which takes as input a potentially modified
// state and returns the original unmodified state.
// This makes sure that any changes made during test steps are discarded but makes sure
// there is a dependency on the intermediate state so buildkit will execute it.
//
// This is implemented using buildkit's passthrough op (via [State.Requires]):
// the returned state's output is `st`, with the intermediate (validation) state
// attached as a build-only dependency.
func WithFinalState(st llb.State, opts ...ValidationOpt) llb.StateOption {
	return trueCmd.WithOutput(st, opts...)
}

func withCheckOutput(filename string, checker *dalec.CheckOutput, opts ...ValidationOpt) []llb.StateOption {
	if checker.IsEmpty() {
		return nil
	}

	var outs []llb.StateOption

	outs = append(outs, checkFileEmpty.WithCheck(filename, checker, opts...))
	outs = append(outs, checkFileEquals.WithCheck(filename, checker, opts...))
	outs = append(outs, checkFileContains.WithCheck(filename, checker, opts...)...)
	outs = append(outs, checkFileMatches.WithCheck(filename, checker, opts...)...)
	outs = append(outs, checkFileStartsWith.WithCheck(filename, checker, opts...))
	outs = append(outs, checkFileEndsWith.WithCheck(filename, checker, opts...))

	return outs
}

func previewString(dt []byte) string {
	if bytes.Contains(dt, []byte{'\x00'}) {
		// Don't try to print binary data.
		// The null byte check is a simple heuristic for binary data.
		// It's not perfect, but good enough for our use case.
		return "<binary data>"
	}

	// dt could be large (especially since these are all mmaped files that get passed in).
	// we don't want to pass this through entirely.
	const maxPreview = 1024
	if len(dt) > maxPreview {
		return string(dt[:maxPreview]) + "<...truncated to 1024 bytes out of " + strconv.Itoa(len(dt)) + " bytes>"
	}
	return string(dt)
}
