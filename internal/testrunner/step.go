package testrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"iter"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/google/shlex"
	"github.com/moby/buildkit/client/llb"
	moby_buildkit_v1_frontend "github.com/moby/buildkit/frontend/gateway/pb"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/pkg/errors"
	"github.com/project-dalec/dalec"
)

const (
	StepRunnerCmdName = "test-steprunner"
	testRunnerPath    = "/tmp/dalec/internal/frontend/test-runner"
	testStepPath      = "/tmp/dalec/internal/frontend/test/step.json"
)

// StepCmd is the entrypoint for the test step runner subcommand.
// It reads the test step from the provided file path (first argument)
// and executes it, writing output to os.Stdout and os.Stderr.
//
// This should only be called from inside a container where the test is meant to run.
func StepCmd(args []string) {
	ctx := appcontext.Context()

	flags := flag.NewFlagSet(StepRunnerCmdName, flag.ExitOnError)
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

	results, err := runStep(ctx, &step, os.Stdout, os.Stderr, stepIndex)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintln(os.Stderr, "error running test step:", err)
		os.Exit(2)
	}

	dt, err = json.Marshal(results)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error marshalling results:", err)
		os.Exit(2)
	}

	if err := writeFileAppend(outputPath, dt, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "error writing test results:", err)
		os.Exit(2)
	}
}

// WithRunStep returns an llb.RunOption that executes the provided test step.
func WithTestStep(frontend llb.State, step *dalec.TestStep, index int, outputPath string) llb.RunOption {
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
		llb.Args([]string{testRunnerPath, StepRunnerCmdName, "--output", outputPath, "--step-index", strconv.Itoa(index), testStepPath}).SetRunOption(ei)
	})
}

// FilterStepError removes extraneous/internal context from errors caused by
// a test step command returning a non-zero exit code.
func FilterStepError(err error) error {
	if err == nil {
		return nil
	}

	var exErr *moby_buildkit_v1_frontend.ExitError
	if !errors.As(err, &exErr) {
		return err
	}
	return &stepCmdError{err: exErr}
}

// runStep executes the provided test step.
// This should only be called from inside a container where the test is meant to run.
//
// Provide the desired stdout and stderr writers to capture output.
func runStep(ctx context.Context, step *dalec.TestStep, stdout, stderr io.Writer, stepIndex int) ([]FileCheckErrResult, error) {
	args, err := shlex.Split(step.Command)
	if err != nil {
		return nil, err
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

	var checks []check

	if !step.Stdout.IsEmpty() {
		buf := bytes.NewBuffer(nil)
		var w io.Writer = buf
		if cmd.Stdout != nil {
			w = io.MultiWriter(cmd.Stdout, buf)
		}
		cmd.Stdout = w
		checks = append(checks, check{buf, step.Stdout, "stdout"})
	}

	if !step.Stderr.IsEmpty() {
		buf := bytes.NewBuffer(nil)
		var w io.Writer = buf
		if cmd.Stderr != nil {
			w = io.MultiWriter(cmd.Stderr, buf)
		}
		cmd.Stderr = w
		checks = append(checks, check{buf, step.Stderr, "stderr"})
	}
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	var results []FileCheckErrResult
	for _, c := range checks {
		if err := c.checker.Check(c.buf.String(), c.name); err != nil {
			results = append(results, FileCheckErrResult{Filename: c.name, StepIndex: &stepIndex, Checks: getFileCheckErrs(err)})
		}
	}
	return results, nil
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

func DecodeResults(r io.Reader) iter.Seq2[*FileCheckErrResult, error] {
	return func(yield func(*FileCheckErrResult, error) bool) {
		dec := json.NewDecoder(r)
		var results []FileCheckErrResult

		for {
			err := dec.Decode(&results)
			if err != nil {
				yield(nil, err)
				return
			}

			for _, r := range results {
				if !yield(&r, nil) {
					return
				}
			}
		}
	}
}
