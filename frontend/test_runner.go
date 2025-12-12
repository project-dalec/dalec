package frontend

import (
	"context"
	"strconv"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
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

		runTests := testrunner.WithTests(target, sOpt, withTestDeps, tests, testrunner.WithConstraints(opts...), TestWithClientFrontend(client))
		return in.With(runTests)
	}
}

func TestWithClientFrontend(client gwclient.Client) testrunner.ValidationOpt {
	return func(vi *testrunner.ValidationInfo) {
		st := getCurrentFrontend(client)
		vi.Frontend = st
	}
}
