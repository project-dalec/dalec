package distro

import (
	"fmt"
	"testing"

	"github.com/project-dalec/dalec"
	"gotest.tools/v3/assert"
)

// determinismRuns is intentionally far above 2: the function under test ranges
// a map, and Go randomizes that order per range, so repeating the call many
// times makes an accidentally-stable order vanishingly unlikely.
const determinismRuns = 25

func TestBuildCandidatePathsIsDeterministic(t *testing.T) {
	t.Parallel()

	deps := make(map[string]dalec.PackageConstraints, 12)
	for i := 0; i < 12; i++ {
		deps[fmt.Sprintf("golang-1.%02d", i)] = dalec.PackageConstraints{}
	}

	t.Run("candidates follow sorted dependency order", func(t *testing.T) {
		want := make([]string, 0, 12)
		for i := 0; i < 12; i++ {
			want = append(want, fmt.Sprintf("/usr/lib/go-1.%02d/bin", i))
		}

		got := buildCandidatePaths(deps, "golang", "/usr/lib/go", "/bin")
		assert.DeepEqual(t, got, want)
	})

	t.Run("output is stable across runs", func(t *testing.T) {
		want := buildCandidatePaths(deps, "golang", "/usr/lib/go", "/bin")
		for i := 1; i < determinismRuns; i++ {
			assert.DeepEqual(t, buildCandidatePaths(deps, "golang", "/usr/lib/go", "/bin"), want)
		}
	})
}
