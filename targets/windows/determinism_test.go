package windows

import (
	"context"
	"fmt"
	"testing"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
	"gotest.tools/v3/assert"
)

// determinismRuns is kept well above 2: regenerating the build script is cheap,
// and each extra run lowers the chance that a randomized map iteration happens
// to repeat its order and hide a regression.
const determinismRuns = 25

// requireDeterministicLLB rebuilds the state each run so the underlying map
// iteration runs again, then asserts the marshaled op definitions match. Only
// Def (the cache-relevant op protos) is compared; Metadata is ignored.
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

func TestCreateBuildScriptProducesDeterministicLLB(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	newSpec := func() *dalec.Spec {
		mkEnv := func() map[string]string {
			env := make(map[string]string, 12)
			for i := 0; i < 12; i++ {
				env[fmt.Sprintf("VAR_%02d", i)] = fmt.Sprintf("value-%d", i)
			}
			return env
		}
		return &dalec.Spec{
			Build: dalec.ArtifactBuild{
				Steps: dalec.BuildStepList{
					{Command: "echo one", Env: mkEnv()},
					{Command: "echo two", Env: mkEnv()},
				},
			},
		}
	}

	build := func() llb.State {
		return createBuildScript(newSpec())
	}

	requireDeterministicLLB(ctx, t, build)
}
