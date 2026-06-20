package distro

import (
	"context"
	"fmt"
	"testing"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
	"gotest.tools/v3/assert"
)

// determinismRuns stays well above 2: building the download LLB is cheap, and
// extra runs make an accidentally-stable randomized map order unlikely enough
// to reliably catch a regression.
const determinismRuns = 25

// requireDeterministicLLB rebuilds the state each run so the underlying map
// iteration executes again, then asserts the marshaled op definitions match.
// Only Def (the cache-relevant op protos) is compared; Metadata is ignored.
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

func TestDownloadDepsProducesDeterministicLLB(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// A context-backed worker keeps the LLB offline so the test exercises only
	// the dnf download argument assembly. InstallFunc is stubbed since the
	// download path runs the worker's bootstrap install before downloading.
	cfg := &Config{
		ContextRef: "worker",
		CacheName:  "test-cache",
		InstallFunc: func(_ *dnfInstallConfig, _ string, pkgs []string) llb.RunOption {
			return llb.Args(append([]string{"install"}, pkgs...))
		},
	}
	sOpt := dalec.SourceOpts{
		GetContext: func(name string, opts ...llb.LocalOption) (*llb.State, error) {
			st := llb.Scratch()
			return &st, nil
		},
	}
	spec := &dalec.Spec{}

	newConstraints := func() dalec.PackageDependencyList {
		deps := make(dalec.PackageDependencyList, 12)
		for i := 0; i < 12; i++ {
			name := fmt.Sprintf("pkg-%02d", i)
			c := dalec.PackageConstraints{}
			if i%2 == 0 {
				c.Version = []string{fmt.Sprintf(">=%d.0", i)}
			}
			deps[name] = c
		}
		return deps
	}

	build := func() llb.State {
		return cfg.DownloadDeps(sOpt, spec, "", newConstraints())
	}

	requireDeterministicLLB(ctx, t, build)
}
