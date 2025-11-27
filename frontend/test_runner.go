package frontend

import (
	"context"
	stderrors "errors"
	"strconv"
	"sync"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/errdefs"
	"github.com/pkg/errors"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/internal/testrunner"
)

// RunTests runs the tests defined in the spec against the given target container.
func RunTests(ctx context.Context, client gwclient.Client, sOpt dalec.SourceOpts, spec *dalec.Spec, withTestDeps llb.StateOption, target string, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		if skipVar := client.BuildOpts().Opts["build-arg:"+"DALEC_SKIP_TESTS"]; skipVar != "" {
			skip, err := strconv.ParseBool(skipVar)
			if err != nil {
				return dalec.ErrorState(in, errors.Wrapf(err, "could not parse build-arg %s", "DALEC_SKIP_TESTS"))
			}
			if skip {
				Warn(ctx, client, llb.Scratch(), "Tests skipped due to build-arg DALEC_SKIP_TESTS="+skipVar)
				return in
			}
		}

		tests := spec.Tests

		t, ok := spec.Targets[target]
		if ok {
			tests = append(tests, t.Tests...)
		}

		if len(tests) == 0 {
			return in
		}

		frontendSt, err := GetCurrentFrontend(client)
		if err != nil {
			// This should never happen and indicates a bug in our implementation.
			// Nothing a user can do about it, so panic.
			panic(err)
		}

		runTests := testrunner.WithTests(target, frontendSt, sOpt, withTestDeps, tests, opts...)
		return in.With(runTests).With(testrunner.WithFinalState(frontendSt, in))
	}
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
