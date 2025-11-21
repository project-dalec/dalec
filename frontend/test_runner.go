package frontend

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"io"
	"io/fs"
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

// RunTests runs the tests defined in the spec against the given target container.
func RunTests(ctx context.Context, client gwclient.Client, spec *dalec.Spec, ref gwclient.Reference, withTestDeps llb.StateOption, target string, platform *ocispecs.Platform) error {
	if skipVar := client.BuildOpts().Opts["build-arg:"+"DALEC_SKIP_TESTS"]; skipVar != "" {
		skip, err := strconv.ParseBool(skipVar)
		if err != nil {
			return errors.Wrapf(err, "could not parse build-arg %s", "DALEC_SKIP_TESTS")
		}
		if skip {
			Warn(ctx, client, llb.Scratch(), "Tests skipped due to build-arg DALEC_SKIP_TESTS="+skipVar)
			return nil
		}
	}

	tests := spec.Tests

	t, ok := spec.Targets[target]
	if ok {
		tests = append(tests, t.Tests...)
	}

	if len(tests) == 0 {
		return nil
	}

	ctr, err := ref.ToState()
	if err != nil {
		return err
	}

	sOpt, err := SourceOptFromClient(ctx, client, platform)
	if err != nil {
		return err
	}

	ctrWithDeps := ctr.With(withTestDeps)

	frontendSt, err := GetCurrentFrontend(client)
	if err != nil {
		// This should never happen and indicates a bug in our implementation.
		// Nothing a user can do about it, so panic.
		panic(err)
	}

	var group errGroup

	const errorsOutputFile = ".errors.txt"
	const outputPath = "/tmp/dalec/internal/test/step/output"
	errorsOutputFullPath := filepath.Join(outputPath, errorsOutputFile)

	for _, test := range tests {
		base := ctr
		for k, v := range test.Env {
			base = base.AddEnv(k, v)
		}

		var opts []llb.RunOption
		pg := llb.ProgressGroup(identity.NewID(), "Test: "+path.Join(target, test.Name), false)
		opts = append(opts, pg)

		for _, sm := range test.Mounts {
			opts = append(opts, sm.ToRunOption(sOpt, pg))
		}

		result := ctrWithDeps
		result = result.File(llb.Mkdir(outputPath, 0o755, llb.WithParents(true)), pg)

		for i, step := range test.Steps {
			opts := append(opts, testrunner.WithTestStep(frontendSt, &step, i, errorsOutputFullPath))
			opts = append(opts, step.GetSourceLocation(result))
			result = result.Run(opts...).Root()
		}

		if len(test.Files) > 0 {
			opts := append(opts, testrunner.WithFileChecks(frontendSt, test, errorsOutputFullPath))

			opts = append(opts, llb.WithCustomNamef("Execute file checks for test: %s", test.Name))
			result = result.Run(opts...).Root()
		}

		group.Go(func() (retErr error) {
			defer func() {
				if r := recover(); r != nil {
					trace := getPanicStack()
					retErr = errors.Errorf("panic running test %q: %v\n%s", test.Name, r, trace)
				}
			}()

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

			dec := json.NewDecoder(f)

			var errs []error
			for {
				var fileCheckResults []testrunner.FileCheckErrResult
				err := dec.Decode(&fileCheckResults)
				if err == io.EOF {
					break
				}
				if err != nil {
					return errors.Wrapf(err, "failed to decode test result for %q", test.Name)
				}

				for _, r := range fileCheckResults {
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

						f, ok := test.Files[r.Filename]
						if ok {
							src := f.GetErrSource(checkErr)
							err = errors.Wrap(checkErr, r.Filename)
							err = errors.Wrapf(err, "%q", test.Name)
							err = wrapWithSource(err, src)
							errs = append(errs, err)
							continue
						}
						errs = append(errs, errors.Wrapf(err, "unknown file %q in test %q, if you see this it is a bug and should be reported", r.Filename, test.Name))
					}
				}
			}

			return stderrors.Join(errs...)
		})
	}

	return group.Wait()
}

func wrapWithSource(err error, src *errdefs.Source) error {
	if src != nil {
		err = errors.Wrapf(err, "%s:%d", src.Info.Filename, src.Ranges[0].Start.Line)
	}
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
