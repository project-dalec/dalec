package testrunner

import (
	"context"
	"fmt"
	"testing"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
	"gotest.tools/v3/assert"
)

func TestFileChecksProduceDeterministicLLB(t *testing.T) {
	spec := &dalec.TestSpec{Files: manyFileChecks()}
	base := llb.Image("example.com/base:latest")

	t.Run("passthrough path requires checks in a stable order", func(t *testing.T) {
		dalec.SetPassthroughOpSupported(true)
		t.Cleanup(func() { dalec.SetPassthroughOpSupported(false) })

		requireDeterministicDef(t, func() *llb.Definition {
			return marshalState(t, base.With(withFileChecks(spec, withTestFrontend())))
		})
	})

	t.Run("fallback path mounts checks in a stable order", func(t *testing.T) {
		dalec.SetPassthroughOpSupported(false)

		requireDeterministicDef(t, func() *llb.Definition {
			return marshalState(t, base.With(withFileChecks(spec, withTestFrontend())))
		})
	})
}

func TestTestEnvProducesDeterministicLLB(t *testing.T) {
	test := &dalec.TestSpec{Env: manyTestEnv()}
	base := llb.Image("example.com/base:latest")

	requireDeterministicDef(t, func() *llb.Definition {
		opts := []ValidationOpt{withTestFrontend(), validationOptsFromTest(test, dalec.SourceOpts{})}
		opt := checkFileNotExists.WithCheck("/check/file", &dalec.FileCheckOutput{NotExist: true}, opts...)
		return marshalState(t, base.With(opt))
	})
}

func marshalState(t *testing.T, st llb.State) *llb.Definition {
	t.Helper()
	def, err := st.Marshal(context.Background())
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	return def
}

// determinismRuns is well above the 2 runs needed to spot a diff: marshaling is
// cheap, and extra runs drive the chance of a randomized map iteration happening
// to repeat its order toward zero.
const determinismRuns = 25

// requireDeterministicDef rebuilds the definition on every run so the underlying
// map iteration executes again, then asserts the marshaled op definitions are
// byte-identical. Only Def (the op protos whose digests drive caching) is
// compared; Metadata carries per-build noise such as progress-group IDs.
func requireDeterministicDef(t *testing.T, build func() *llb.Definition) {
	t.Helper()

	want := build()
	for i := 1; i < determinismRuns; i++ {
		got := build()
		assert.DeepEqual(t, got.Def, want.Def)
	}
}

func manyFileChecks() map[string]dalec.FileCheckOutput {
	files := make(map[string]dalec.FileCheckOutput, 12)
	for i := 0; i < 12; i++ {
		files[fmt.Sprintf("/check/file-%02d", i)] = dalec.FileCheckOutput{NotExist: true}
	}
	return files
}

func manyTestEnv() map[string]string {
	env := make(map[string]string, 12)
	for i := 0; i < 12; i++ {
		env[fmt.Sprintf("VAR_%02d", i)] = fmt.Sprintf("value-%d", i)
	}
	return env
}
