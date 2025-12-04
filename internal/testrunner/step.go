package testrunner

import (
	"context"
	_ "crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/shlex"
	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"
	"github.com/project-dalec/dalec"
)

const (
	trueCmd    = noopCommand("true")
	stepRunner = stepRunnerCommand("step-runner")
)

type noopCommand string

func (c noopCommand) Cmd(args []string) {}

// WithOutput runs a no-op command that produces the provided output state.
// This is useful for creating a dependency between the StateOption's input
// state and the provided output state.
func (c noopCommand) WithOutput(out llb.State, opts ...ValidationOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		const outputPath = "/tmp/internal/dalec/testrunner/__internal_output__"
		args := []string{string(c)}

		// Ideally we would use llb.Readonly here.
		// However, buildkit optmizes out the case since the returned state
		// cannot be modified by the run.
		// The run ends up not executing.
		return in.Run(
			testRunner(args, opts...),
		).AddMount(outputPath, out)
	}
}

type stepRunnerCommand string

func (c stepRunnerCommand) stepJSONPath() string {
	const p = "/tmp/internal/dalec/testrunner/step/step.json"
	return p
}

func (c stepRunnerCommand) Cmd(ctx context.Context, args []string) {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "usage: "+string(c))
		os.Exit(1)
	}

	dt, err := os.ReadFile(c.stepJSONPath())
	if err != nil {
		fmt.Fprintln(os.Stderr, "error reading test step:", err)
		return
	}

	var step dalec.TestStep
	if err := json.Unmarshal(dt, &step); err != nil {
		fmt.Fprintln(os.Stderr, "error unmarshalling test step:", err)
		return
	}

	err = c.doStep(ctx, &step, os.Stdout, os.Stderr, c.outputPath())
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintln(os.Stderr, "error running test step:", err)
		os.Exit(2)
	}

}

func (c stepRunnerCommand) outputPath() string {
	const p = "/tmp/internal/dalec/testrunner/step/output"
	return p
}

func (c stepRunnerCommand) Run(test *dalec.TestSpec, sOpt dalec.SourceOpts, opts ...ValidationOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		out := in

		var runOpts []llb.RunOption
		for _, mount := range test.Mounts {
			runOpts = append(runOpts, mount.ToRunOption(sOpt, asConstraints(opts...)))
		}
		for k, v := range test.Env {
			runOpts = append(runOpts, llb.AddEnv(k, v))
		}

		// Steps run sequentially, each step depending on the previous one.
		for _, step := range test.Steps {
			out = out.With(c.withTestStep(step, runOpts, opts...))
		}

		// Each step can modify the state, but we want to discard those changes for the set of steps.
		// We still want to have a dependency on the final state so that buildkit
		// executes the steps.
		return out.With(WithFinalState(in, opts...))
	}
}

func (c stepRunnerCommand) withTestStep(step dalec.TestStep, runOpts []llb.RunOption, opts ...ValidationOpt) llb.StateOption {
	return func(in llb.State) llb.State {

		dt, err := json.Marshal(step)
		if err != nil {
			return dalec.ErrorState(in, fmt.Errorf("could not marshal test step %q: %w", step.Command, err))
		}

		st := llb.Scratch().File(llb.Mkfile("step.json", 0o600, dt), asConstraints(opts...))

		args := []string{
			string(c),
		}

		out := in.Run(
			dalec.WithRunOptions(runOpts...),
			dalec.RunOptFunc(func(ei *llb.ExecInfo) {
				for k, v := range step.Env {
					ei.State = ei.State.AddEnv(k, v)
				}
			}),
			llb.AddMount(c.stepJSONPath(), st, llb.SourcePath("step.json")),
			testRunner(args, opts...),
			step.GetSourceLocation(in),
			llb.WithCustomName(step.Command),
		).Root()

		// Do any stdout/stderr checks
		var stateOpts []llb.StateOption
		stateOpts = append(stateOpts, withCheckOutput(filepath.Join(c.outputPath(), "stdout"), &step.Stdout, opts...)...)
		stateOpts = append(stateOpts, withCheckOutput(filepath.Join(c.outputPath(), "stderr"), &step.Stderr, opts...)...)

		return out.With(mergeStateOptions(stateOpts, opts...))
	}
}

// WithFinalState returns a state option which takes as input a potentially modified
// state and returns the original unmodified state.
// This makes sure that any changes made during test steps are discarded but makes sure
// there is a dependency on the intermediate state so buildkit will execute it.
//
// NOTE: This is a hack to work around the fact that buildkit does not currently
// have a proper way to express "run this for validation only".
func WithFinalState(st llb.State, opts ...ValidationOpt) llb.StateOption {
	return trueCmd.WithOutput(st, opts...)
}

func withCheckOutput(filename string, checker *dalec.CheckOutput, opts ...ValidationOpt) []llb.StateOption {
	if checker.IsEmpty() {
		return nil
	}

	var outs []llb.StateOption

	outs = append(outs, checkFileEmpty.Validate(filename, checker, opts...))
	outs = append(outs, checkFileEquals.Validate(filename, checker, opts...))
	outs = append(outs, checkFileContains.Validate(filename, checker, opts...)...)
	outs = append(outs, checkFileMatches.Validate(filename, checker, opts...)...)
	outs = append(outs, checkFileStartsWith.Validate(filename, checker, opts...))
	outs = append(outs, checkFileEndsWith.Validate(filename, checker, opts...))

	return outs
}

// runStep executes the provided test step.
// This should only be called from inside a container where the test is meant to run.
//
// Provide the desired stdout and stderr writers to capture output.
func (stepRunnerCommand) doStep(ctx context.Context, step *dalec.TestStep, stdout, stderr io.Writer, outputPath string) error {
	args, err := shlex.Split(step.Command)
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if step.Stdin != "" {
		cmd.Stdin = strings.NewReader(step.Stdin)
	}

	if !step.Stdout.IsEmpty() {
		if err := os.MkdirAll(outputPath, 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(filepath.Join(outputPath, "stdout"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		cmd.Stdout = io.MultiWriter(cmd.Stdout, f)
	}

	if !step.Stderr.IsEmpty() {
		if err := os.MkdirAll(outputPath, 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(filepath.Join(outputPath, "stderr"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		cmd.Stderr = io.MultiWriter(cmd.Stderr, f)
	}

	if err := cmd.Run(); err != nil {
		return err
	}

	return nil
}
