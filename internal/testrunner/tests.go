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

		outs := make([]llb.StateOption, 0, len(tests))
		for _, test := range tests {
			outs = append(outs, withTest(target, sOpt, test, opts...))
		}

		out := in.With(deps).With(mergeStateOptions(outs, opts...))
		return out.With(WithFinalState(in, opts...))
	}
}

func withTest(target string, sOpt dalec.SourceOpts, test *dalec.TestSpec, opts ...ValidationOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		opts := append(opts, WithConstraints(dalec.ProgressGroup("Test: "+path.Join(target, test.Name))))
		opts = append(opts, valiationOptsFromTest(test, sOpt))

		runSteps := stepRunner.Run(test, sOpt, opts...)
		fileChecks := withFileChecks(test, opts...)
		out := in.With(runSteps).With(fileChecks)

		return out.With(WithFinalState(in, opts...))
	}
}
