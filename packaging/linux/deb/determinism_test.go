package deb

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
	"gotest.tools/v3/assert"
)

// determinismRuns is intentionally well above the 2 runs needed to spot an
// obvious diff: marshaling is cheap, and extra runs drive the chance of a
// randomized map iteration happening to repeat its order toward zero.
const determinismRuns = 25

// requireDeterministicLLB rebuilds the state on each run so the underlying map
// iteration executes again, then asserts the marshaled op definitions are
// byte-identical. Only Def (the op protos whose digests drive caching) is
// compared; Metadata carries per-build noise and is ignored.
func requireDeterministicLLB(ctx context.Context, t *testing.T, build func() llb.State) {
	t.Helper()

	want, err := build().Marshal(ctx)
	assert.NilError(t, err)

	for i := 1; i < determinismRuns; i++ {
		got, err := build().Marshal(ctx)
		assert.NilError(t, err)
		assert.DeepEqual(t, got.Def, want.Def)
	}
}

func manySourceStates() map[string]llb.State {
	states := make(map[string]llb.State, 12)
	for i := 0; i < 12; i++ {
		name := fmt.Sprintf("src-%02d", i)
		states[name] = llb.Scratch().File(llb.Mkfile("/"+name, 0o644, []byte(name)))
	}
	return states
}

func TestMountSourcesProducesDeterministicLLB(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	build := func() llb.State {
		runOpt := mountSources(manySourceStates(), "/src", nil)
		return llb.Scratch().Run(llb.Args([]string{"true"}), runOpt).Root()
	}

	// mountSources sorts its mounts so the result is deterministic regardless of
	// BuildKit internals. This guards that observable property end to end.
	requireDeterministicLLB(ctx, t, build)
}

func TestTarDebSourcesProducesDeterministicLLB(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	spec := &dalec.Spec{}
	sOpt := dalec.SourceOpts{
		GetContext: func(name string, opts ...llb.LocalOption) (*llb.State, error) {
			st := llb.Local(name, opts...)
			return &st, nil
		},
	}

	build := func() llb.State {
		return TarDebSources(llb.Scratch(), spec, manySourceStates(), "/sources.tar", sOpt)
	}

	requireDeterministicLLB(ctx, t, build)
}

func TestRulesEnvProducesDeterministicOutput(t *testing.T) {
	t.Parallel()

	newWrapper := func() *rulesWrapper {
		env := make(map[string]string, 12)
		for i := 0; i < 12; i++ {
			env[fmt.Sprintf("VAR_%02d", i)] = fmt.Sprintf("value-%d", i)
		}
		return &rulesWrapper{
			Spec: &dalec.Spec{Build: dalec.ArtifactBuild{Env: env}},
		}
	}

	t.Run("env exports are sorted by key", func(t *testing.T) {
		want := &strings.Builder{}
		for i := 0; i < 12; i++ {
			fmt.Fprintf(want, "export VAR_%02d := value-%d\n", i, i)
		}
		assert.Equal(t, newWrapper().Envs().String(), want.String())
	})

	t.Run("rendered output is stable across runs", func(t *testing.T) {
		want := newWrapper().Envs().String()
		for i := 1; i < determinismRuns; i++ {
			assert.Equal(t, newWrapper().Envs().String(), want)
		}
	})
}
