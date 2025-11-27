package testrunner

import (
	"context"
	_ "crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/shlex"
	"github.com/moby/buildkit/client/llb"
	moby_buildkit_v1_frontend "github.com/moby/buildkit/frontend/gateway/pb"
	"github.com/pkg/errors"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/internal/commands"
	"github.com/project-dalec/dalec/internal/plugins"
)

func init() {
	commands.RegisterPlugin(stepRunnerCmdName, plugins.CmdHandlerFunc(stepCmd))
	commands.RegisterPlugin(trueCmdName, plugins.CmdHandlerFunc(cmdTrue))
}

func cmdTrue(_ context.Context, _ []string) {}

const (
	stepRunnerCmdName = "test-steprunner"
	trueCmdName       = "true"

	testRunnerPath = "/tmp/dalec/internal/frontend/internal-test-runner"
	testStepPath   = "/tmp/dalec/internal/frontend/test/step.json"
)

// StepCmd is the entrypoint for the test step runner subcommand.
// It reads the test step from the provided file path (first argument)
// and executes it, writing output to os.Stdout and os.Stderr.
//
// This should only be called from inside a container where the test is meant to run.
func stepCmd(ctx context.Context, args []string) {
	flags := flag.NewFlagSet(stepRunnerCmdName, flag.ExitOnError)
	var outputPath string
	flags.StringVar(&outputPath, "output", "", "Path to write test results to")

	var stepIndex int
	flags.IntVar(&stepIndex, "step-index", -1, "Index of the step being run")

	if err := flags.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "error parsing flags:", err)
		os.Exit(1)
	}

	if stepIndex < 0 {
		fmt.Fprintln(os.Stderr, "--step-index is required")
		os.Exit(1)
	}

	dt, err := os.ReadFile(flags.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error reading test step:", err)
		return
	}

	var step dalec.TestStep
	if err := json.Unmarshal(dt, &step); err != nil {
		fmt.Fprintln(os.Stderr, "error unmarshalling test step:", err)
		return
	}

	err = runStep(ctx, &step, os.Stdout, os.Stderr, outputPath)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintln(os.Stderr, "error running test step:", err)
		os.Exit(2)
	}

}

func stepRunner(frontend llb.State, step *dalec.TestStep, index int, outputPath string) llb.RunOption {
	return dalec.RunOptFunc(func(ei *llb.ExecInfo) {
		llb.WithCustomName(step.Command).SetRunOption(ei)

		dt, err := json.Marshal(step)
		if err != nil {
			ei.State = dalec.ErrorState(ei.State, fmt.Errorf("failed to marshal test step %q: %w", step.Command, err))
			llb.Args([]string{"false"}).SetRunOption(ei)
			return
		}

		for k, v := range step.Env {
			ei.State = ei.State.AddEnv(k, v)
		}

		llb.AddMount(testRunnerPath, frontend, llb.SourcePath("/frontend")).SetRunOption(ei)

		st := llb.Scratch().File(llb.Mkfile("step.json", 0o600, dt))
		llb.AddMount(testStepPath, st, llb.SourcePath("step.json")).SetRunOption(ei)
		llb.Args([]string{testRunnerPath, stepRunnerCmdName, "--output", outputPath, "--step-index", strconv.Itoa(index), testStepPath}).SetRunOption(ei)
	})
}

func nullOutput(frontend llb.State) llb.StateOption {
	return WithFinalState(frontend, llb.Scratch())
}

// WithFinalState returns a state option which takes as input a potentially modified
// state and returns the original unmodified state.
// This makes sure that any changes made during test steps are discarded but makes sure
// there is a dependency on the intermediate state so buildkit will execute it.
//
// NOTE: This is a hack to work around the fact that buildkit does not currently
// have a proper way to express "run this for validation only".
func WithFinalState(frontend, st llb.State) llb.StateOption {
	return func(in llb.State) llb.State {
		const outputPath = "/tmp/internal/dalec/testrunner/step/output"
		return in.Run(
			llb.AddMount(testRunnerPath, frontend, llb.SourcePath("/frontend")),
			llb.Args([]string{testRunnerPath, trueCmdName}),
		).
			// Ideally we would use llb.Readonly here.
			// However, buildkit optmizes out the case since the returned state
			// cannot be modified by the run.
			// The run ends up not executing.
			AddMount(outputPath, st)
	}
}

func withTestStep(frontend llb.State, step *dalec.TestStep, index int, runOpts []llb.RunOption, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		const outputPath = "/tmp/internal/dalec/testrunner/step/output"
		runStep := stepRunner(frontend, step, index, outputPath)
		out := in.Run(
			runStep,
			dalec.WithRunOptions(runOpts...),
			dalec.WithConstraints(opts...),
			step.GetSourceLocation(in),
		).Root()

		var states []llb.State

		if !step.Stdout.IsEmpty() {
			st := out.With(withCheckOutput(frontend, filepath.Join(outputPath, "stdout"), &step.Stdout, opts...))
			st = st.File(llb.Rm(outputPath))
			states = append(states, st)
		}

		if !step.Stderr.IsEmpty() {
			st := out.With(withCheckOutput(frontend, filepath.Join(outputPath, "stderr"), &step.Stderr, opts...))
			st = st.File(llb.Rm(outputPath))
			states = append(states, st)
		}

		if len(states) > 0 {
			out = dalec.MergeAtPath(in, states, "/", opts...)
		}

		return out
	}
}

func withCheckOutput(frontend llb.State, filename string, checker *dalec.CheckOutput, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		out := in
		var states []llb.State

		newCheck := func(kind, filename, checkValue string, index int, opts ...llb.ConstraintsOpt) llb.StateOption {
			return func(in llb.State) llb.State {
				return in.Run(
					llb.AddMount(testRunnerPath, frontend, llb.SourcePath("/frontend")),
					llb.Args([]string{testRunnerPath, fileCheckerCmdName, kind, filename, checkValue}),
					dalec.WithConstraints(opts...),
					checker.GetSourceLocation(in, kind, index),
				).Root()
			}
		}

		if checker.Empty {
			st := out.With(newCheck(dalec.CheckOutputEmptyKind, filename, "", 0, opts...))
			states = append(states, st)
		}

		if len(checker.Contains) > 0 {
			for i, v := range checker.Contains {
				st := out.With(newCheck(dalec.CheckOutputContainsKind, filename, v, i, opts...))
				states = append(states, st)
			}
		}

		if len(checker.Matches) > 0 {
			for i, v := range checker.Matches {
				st := out.With(newCheck(dalec.CheckOutputMatchesKind, filename, v, i, opts...))
				states = append(states, st)
			}
		}

		if checker.StartsWith != "" {
			st := out.With(newCheck(dalec.CheckOutputStartsWithKind, filename, checker.StartsWith, 0, opts...))
			states = append(states, st)
		}

		if checker.EndsWith != "" {
			st := out.With(newCheck(dalec.CheckOutputEndsWithKind, filename, checker.EndsWith, 0, opts...))
			states = append(states, st)
		}

		if len(states) > 0 {
			out = dalec.MergeAtPath(in, states, "/", opts...)
		}
		return out
	}
}

// runStep executes the provided test step.
// This should only be called from inside a container where the test is meant to run.
//
// Provide the desired stdout and stderr writers to capture output.
func runStep(ctx context.Context, step *dalec.TestStep, stdout, stderr io.Writer, outputPath string) error {
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

	type check struct {
		buf     fmt.Stringer
		checker dalec.CheckOutput
		name    string
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

type stepCmdError struct {
	err *moby_buildkit_v1_frontend.ExitError
}

func (s *stepCmdError) Error() string {
	return fmt.Sprintf("step did not complete successfully: exit code: %d", s.err.ExitCode)
}

func (s *stepCmdError) Unwrap() error {
	return s.err
}
