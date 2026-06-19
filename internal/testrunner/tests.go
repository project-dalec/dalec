package testrunner

import (
	"path"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
)

func WithTests(target string, sOpt dalec.SourceOpts, deps llb.StateOption, tests []*dalec.TestSpec, opts ...ValidationOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		if len(tests) == 0 {
			return in
		}

		base := in.With(deps)

		testStates := make([]llb.State, 0, len(tests))
		for _, test := range tests {
			testStates = append(testStates, base.With(withTest(target, sOpt, test, opts...)))
		}

		// Return the original (untested) state while requiring every test to be
		// evaluated; see [requireStates] for how that dependency is forced.
		return requireStates(testsRequiresID, in, testStates, opts...)
	}
}

func withTest(target string, sOpt dalec.SourceOpts, test *dalec.TestSpec, opts ...ValidationOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		opts := append(opts, WithConstraints(dalec.ProgressGroup("Test: "+path.Join(target, test.Name))))
		opts = append(opts, validationOptsFromTest(test, sOpt))

		runSteps := stepRunner.Run(test, sOpt, opts...)
		fileChecks := withFileChecks(test, opts...)
		out := in.With(runSteps).With(fileChecks)

		return out.With(WithFinalState(in, opts...))
	}
}
