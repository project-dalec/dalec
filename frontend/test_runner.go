package frontend

import (
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/solver/errdefs"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend/pkg/bkfs"
	"github.com/project-dalec/dalec/internal/testrunner"
)

const (
	testErrorsOutputFile = ".errors.txt"
	testOutputPath       = "/tmp/dalec/internal/test/step/output"
)

var errorsOutputFullPath = filepath.Join(testOutputPath, testErrorsOutputFile)

// RunTests runs the tests defined in the spec against the given the input state.
// The result of this is either the provided `finalState` or a state that errors when executed with the errors produced by the tests.
// The input state should be a runnable container with all dependencies already installed.
func RunTests(ctx context.Context, client gwclient.Client, spec *dalec.Spec, finalState llb.State, target string, platform *ocispecs.Platform) llb.StateOption {
	return func(in llb.State) llb.State {
		const (
			buildArgPrefix = "build-arg:"
			skipTestsKey   = "DALEC_SKIP_TESTS"
		)
		if skipVar := client.BuildOpts().Opts[buildArgPrefix+skipTestsKey]; skipVar != "" {
			skip, err := strconv.ParseBool(skipVar)
			if err != nil {
				err = errors.Wrapf(err, "could not parse %s=%s", skipTestsKey, skipVar)
				return dalec.ErrorState(finalState, err)
			}
			if skip {
				Warnf(ctx, client, llb.Scratch(), "Tests skipped due to build-arg %s=%s", skipTestsKey, skipVar)
				return finalState
			}
		}

		tests := spec.Tests

		t, ok := spec.Targets[target]
		if ok {
			tests = append(tests, t.Tests...)
		}

		if len(tests) == 0 {
			return finalState
		}

		if !evalState(ctx, client, in) {
			return in
		}

		sOpt, err := SourceOptFromClient(ctx, client, platform)
		if err != nil {
			return dalec.ErrorState(finalState, err)
		}

		frontendSt, err := GetCurrentFrontend(client)
		if err != nil {
			// This should never happen and indicates a bug in our implementation.
			// Nothing a user can do about it, so panic.
			panic(err)
		}

		var group errGroup

		for _, test := range tests {
			pg := llb.ProgressGroup(identity.NewID(), "Test: "+path.Join(target, test.Name), false)

			result := in.With(runTest(frontendSt, sOpt, test, pg))

			group.Go(func() (retErr error) {
				defer func() {
					if r := recover(); r != nil {
						trace := getPanicStack()
						retErr = errors.Errorf("panic running test %q: %v\n%s", test.Name, r, trace)
					}
				}()

				return checkTestResult(ctx, client, test, result, platform)
			})
		}

		err = group.Wait()
		if err == nil {
			return finalState
		}

		return in.With(withTestError(err, frontendSt))
	}
}

func checkTestResult(ctx context.Context, client gwclient.Client, test *dalec.TestSpec, result llb.State, platform *ocispecs.Platform) error {
	// Make sure we force evaluation here otherwise errors won't surface until
	// later, e.g. when we try to read the output file.
	resultFS, err := bkfs.EvalFromState(ctx, &result, client, dalec.Platform(platform))
	if err != nil {
		err = testrunner.FilterStepError(err)
		return errors.Wrapf(err, "%q", test.Name)
	}

	p := strings.TrimPrefix(errorsOutputFullPath, "/")
	f, err := resultFS.Open(p)
	if err != nil {
		if !stderrors.Is(err, fs.ErrNotExist) {
			return errors.Wrapf(err, "failed to read test result for %q", test.Name)
		}
		// No errors file means no errors.
		return nil
	}
	defer f.Close()

	var errs []error
	for r, err := range testrunner.DecodeResults(f) {
		if err != nil {
			if err == io.EOF {
				break
			}
			return errors.Wrapf(err, "failed to decode test result for %q", test.Name)
		}

		for _, checkErr := range r.Checks {
			var src *errdefs.Source
			if r.StepIndex != nil {
				idx := *r.StepIndex
				step := test.Steps[idx]
				err := errors.Wrapf(checkErr, "step %d", idx)
				err = errors.Wrapf(err, "%q", test.Name)
				switch r.Filename {
				case "stdout":
					src = step.Stdout.GetErrSource(checkErr)
				case "stderr":
					src = step.Stderr.GetErrSource(checkErr)
				default:
					return errors.Wrapf(err, "unknown output stream name for step command check, if you see this it is a bug and should be reported: stream %q", r.Filename)
				}

				err = wrapWithSource(err, src)
				errs = append(errs, err)
				continue
			}

			var err error = checkErr
			f, ok := test.Files[r.Filename]
			if ok {
				src := f.GetErrSource(checkErr)
				err = errors.Wrap(err, r.Filename)
				err = errors.Wrapf(err, "%q", test.Name)
				err = wrapWithSource(err, src)
				errs = append(errs, err)
				continue
			}
			errs = append(errs, errors.Wrapf(err, "unknown file %q in test %q, if you see this it is a bug and should be reported", r.Filename, test.Name))
		}
	}

	return stderrors.Join(errs...)
}

func runTest(frontend llb.State, sOpt dalec.SourceOpts, test *dalec.TestSpec, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		base := in

		errorsOutputFullPath := filepath.Join(testOutputPath, testErrorsOutputFile)

		for k, v := range test.Env {
			base = base.AddEnv(k, v)
		}

		var rOpts []llb.RunOption
		rOpts = append(rOpts, dalec.WithConstraints(opts...))

		for _, sm := range test.Mounts {
			rOpts = append(rOpts, sm.ToRunOption(sOpt, dalec.WithConstraints(opts...)))
		}

		result := base
		result = result.File(llb.Mkdir(testOutputPath, 0o755, llb.WithParents(true)), opts...)

		for i, step := range test.Steps {
			rOpts := append(rOpts, testrunner.WithTestStep(frontend, &step, i, errorsOutputFullPath))
			rOpts = append(rOpts, step.GetSourceLocation(result))
			result = result.Run(rOpts...).Root()
		}

		if len(test.Files) > 0 {
			rOpts := append(rOpts, testrunner.WithFileChecks(frontend, test, errorsOutputFullPath))

			rOpts = append(rOpts, llb.WithCustomNamef("Execute file checks for test: %s", test.Name))
			result = result.Run(rOpts...).Root()
		}

		return result
	}
}

func withTestError(err error, frontendSt llb.State) llb.StateOption {
	return func(in llb.State) llb.State {
		// Write the error(s) to a file then run a command which will:
		// 1. cat the errors to stderr
		// 2. exit non-zero to trigger a build failure.
		const (
			frontendP = "/tmp/internal/dalec/frontend"
			errorsP   = "/tmp/internal/dalec/test/output/errors.txt"
		)
		errorsSt := llb.Scratch().File(
			llb.Mkfile(filepath.Base(errorsP), 0o644, []byte(err.Error())),
		)

		return in.Run(
			llb.WithCustomName("Report test errors"),
			llb.AddMount(frontendP, frontendSt, llb.Readonly, llb.SourcePath("/frontend")),
			llb.AddMount(errorsP, errorsSt, llb.Readonly, llb.SourcePath("/errors.txt")),
			llb.Args([]string{frontendP, TestErrorCmdName, errorsP}),
			dalec.RunOptFunc(func(ei *llb.ExecInfo) {
				// Add the source maps for each error
				c := ei.Constraints

				for _, src := range errdefs.Sources(err) {
					if src.Info == nil {
						continue
					}
					sm := llb.NewSourceMap(&in, src.Info.Filename, src.Info.Language, src.Info.Data)
					sm.Location(src.Ranges).SetConstraintsOption(&c)
				}

				ei.Constraints = c
			}),
		).Root()
	}
}

func wrapWithSource(err error, src *errdefs.Source) error {
	if src == nil {
		return err
	}

	err = errors.Wrapf(err, "%s:%d", src.Info.Filename, src.Ranges[0].Start.Line)
	return errdefs.WithSource(err, src)
}

type errGroup struct {
	group sync.WaitGroup
	mu    sync.Mutex
	errs  []error
}

func (g *errGroup) Go(f func() error) {
	g.group.Add(1)

	go func() {
		defer g.group.Done()
		err := f()
		g.mu.Lock()
		g.errs = append(g.errs, err)
		g.mu.Unlock()
	}()
}

func (g *errGroup) Wait() error {
	g.group.Wait()
	g.mu.Lock()
	defer g.mu.Unlock()

	err := stderrors.Join(g.errs...)

	g.errs = g.errs[:0]
	return err
}

const TestErrorCmdName = "test-error-cmd"

// TestErrorCmd is a helper command that reads an error message from a file and
// writes it to stderr before exiting with a non-zero code.
// It is used by RunTests to report test failures.
func TestErrorCmd(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "requires exactly one argument: path to error file")
		os.Exit(2)
	}
	p := args[0]
	f, err := os.Open(p)
	if err != nil {
		panic(err)
	}

	defer f.Close()

	_, err = io.Copy(os.Stderr, f)
	if err != nil {
		panic(err)
	}

	os.Exit(1)
}

func evalState(ctx context.Context, client gwclient.Client, st llb.State) bool {
	def, err := st.Marshal(ctx)
	if err != nil {
		return false
	}

	_, err = client.Solve(ctx, gwclient.SolveRequest{
		Definition: def.ToPB(),
		Evaluate:   true,
	})

	return err == nil
}
