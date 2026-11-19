package test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/project-dalec/dalec"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

// testImageLayers tests that target, a container target built without a base
// image, splits the image into layers by package set: base packages, runtime
// dependencies, the spec's packages and a final state layer.
func testImageLayers(ctx context.Context, t *testing.T, target string) {
	t.Helper()

	const (
		basePrefix  = "dalec: base packages: "
		depsPrefix  = "dalec: runtime dependencies: "
		appPrefix   = "dalec: packages: "
		stateLayer  = "dalec: package database and generated files"
		layerPrefix = "dalec: "
	)

	// layerPackages returns the packages of each layer set in the image
	// history, in order.
	layerPackages := func(t *testing.T, res *gwclient.Result) ([]string, map[string][]string) {
		t.Helper()
		dt, ok := res.Metadata[exptypes.ExporterImageConfigKey]
		assert.Assert(t, ok, "missing image config")
		var img dalec.DockerImageSpec
		assert.NilError(t, json.Unmarshal(dt, &img))

		var order []string
		pkgs := make(map[string][]string)
		for _, h := range img.History {
			if !strings.HasPrefix(h.CreatedBy, layerPrefix) {
				continue
			}
			for _, prefix := range []string{basePrefix, depsPrefix, appPrefix} {
				if v, ok := strings.CutPrefix(h.CreatedBy, prefix); ok {
					order = append(order, prefix)
					pkgs[prefix] = strings.Split(v, ", ")
				}
			}
			if h.CreatedBy == stateLayer {
				order = append(order, stateLayer)
			}
		}
		return order, pkgs
	}

	newSpec := func(t *testing.T, rev string) dalec.Spec {
		spec := testLinuxSpec(t, dalec.Spec{})
		spec.Revision = rev
		spec.Artifacts = dalec.Artifacts{
			Binaries: map[string]dalec.ArtifactConfig{"src/hello": {}},
		}
		spec.Sources = map[string]dalec.Source{
			"src": {Inline: &dalec.SourceInline{Dir: &dalec.SourceInlineDir{
				Files: map[string]*dalec.SourceInlineFile{"hello": {Contents: "#!/bin/bash\necho hello\n", Permissions: 0o755}},
			}}},
		}
		spec.Tests = []*dalec.TestSpec{{
			Name: "app runs from the merged layers",
			Steps: []dalec.TestStep{{
				Command: "/usr/bin/hello",
				Stdout:  dalec.CheckOutput{Contains: []string{"hello"}},
			}},
		}}
		return spec
	}

	t.Run("package sets", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)

		// Rebuilding the package gives its files new timestamps but the same
		// contents. The second build must use the new timestamps instead of
		// reusing a cached layer from the first build, which the layer
		// verification step checks.
		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
			for _, rev := range []string{"1", "2"} {
				spec := newSpec(t, rev)
				res := solveT(ctx, t, gwc, newSolveRequest(withSpec(ctx, t, &spec), withBuildTarget(target)))

				order, pkgs := layerPackages(t, res)
				t.Logf("layers: %v", pkgs)
				// Sets without files have no layer, but the spec package and
				// the package database always have one.
				var want []string
				for _, l := range []string{basePrefix, depsPrefix, appPrefix, stateLayer} {
					if _, ok := pkgs[l]; ok || l == appPrefix || l == stateLayer {
						want = append(want, l)
					}
				}
				assert.Check(t, cmp.DeepEqual(order, want))
				assert.Check(t, cmp.DeepEqual(pkgs[appPrefix], []string{spec.Name}))

				// bash is a runtime dependency of the spec package, which
				// may also be a dependency of the base packages.
				inBase := strings.Contains(", "+strings.Join(pkgs[basePrefix], ", ")+",", ", bash,")
				inDeps := strings.Contains(", "+strings.Join(pkgs[depsPrefix], ", ")+",", ", bash,")
				assert.Check(t, inBase != inDeps, "bash must be in exactly one of the base and runtime dependency layers: %v", pkgs)
			}
		})
	})
}
