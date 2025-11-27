package testrunner

import (
	"path"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
)

func WithTests(target string, frontend llb.State, sOpt dalec.SourceOpts, deps llb.StateOption, tests []*dalec.TestSpec, opts ...llb.ConstraintsOpt) llb.StateOption {
	if len(tests) == 0 {
		return dalec.NoopStateOption
	}

	const mntPath = "/tmp/internal/dalec/tests/runner/__internal_state__"

	return func(in llb.State) llb.State {
		withDeps := in.With(deps)

		states := make([]llb.State, len(tests))
		for _, test := range tests {
			st := handleTest(target, frontend, sOpt, withDeps, test, opts...)
			states = append(states, st)
		}

		return dalec.MergeAtPath(in, states, "/")
	}
}

func handleTest(target string, frontend llb.State, sOpt dalec.SourceOpts, in llb.State, test *dalec.TestSpec, opts ...llb.ConstraintsOpt) llb.State {
	out := in
	for k, v := range test.Env {
		out = out.AddEnv(k, v)
	}

	opts = append(opts, dalec.ProgressGroup("Test: "+path.Join(target, test.Name)))

	var runOpts []llb.RunOption
	for _, mount := range test.Mounts {
		runOpts = append(runOpts, mount.ToRunOption(sOpt, dalec.WithConstraints(opts...)))
	}

	for i, step := range test.Steps {
		out = out.With(withTestStep(frontend, &step, i, runOpts, opts...))
	}

	if len(test.Files) > 0 {
		check := withFileChecks(frontend, test, opts...)
		out = out.With(check)
	}

	return out.With(nullOutput(frontend))
}
