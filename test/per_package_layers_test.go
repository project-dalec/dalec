package test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/internal/layersplit"
	"github.com/project-dalec/dalec/internal/test"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func testRPMPerPackageLayers(ctx context.Context, t *testing.T, testConfig testLinuxConfig) {
	t.Helper()

	target := testConfig.Target.MinimalContainer

	layerGroups := func(t *testing.T, res *gwclient.Result) []layersplit.Group {
		t.Helper()
		dt, ok := res.Metadata[exptypes.ExporterImageConfigKey]
		assert.Assert(t, ok, "missing image config")
		var img dalec.DockerImageSpec
		assert.NilError(t, json.Unmarshal(dt, &img))
		assert.Check(t, cmp.Equal(img.Config.Labels[layersplit.LabelImageLayers], dalec.ImageLayersPerPackage))

		var groups []layersplit.Group
		for _, h := range img.History {
			if g, ok := layersplit.ParseHistoryComment(h.Comment); ok {
				groups = append(groups, g)
			}
		}
		return groups
	}

	checkGroups := func(t *testing.T, spec *dalec.Spec, groups []layersplit.Group) {
		t.Helper()
		assert.Assert(t, len(groups) > 2, "expected more than two layers, got %v", groups)
		assert.Check(t, cmp.Equal(groups[len(groups)-1].Kind, layersplit.KindState))

		var foundApp, foundBash bool
		for _, g := range groups {
			switch {
			case g.Kind == layersplit.KindApp && g.Name == spec.Name:
				foundApp = true
			case g.Kind == layersplit.KindOS && strings.Contains(strings.Join(g.Packages, ","), "bash"):
				foundBash = true
			}
		}
		assert.Check(t, foundApp, "missing app layer in %v", groups)
		assert.Check(t, foundBash, "missing bash layer in %v", groups)
	}

	// A previous image context without an image config or package information.
	notAnImage := llb.Scratch().File(llb.Mkfile("/marker", 0o644, []byte("not an image\n")))

	newSpec := func(t *testing.T) dalec.Spec {
		spec := testLinuxSpec(t, dalec.Spec{})
		spec.Artifacts = dalec.Artifacts{
			Binaries: map[string]dalec.ArtifactConfig{"src/hello": {}},
		}
		spec.Sources = map[string]dalec.Source{
			"src": {Inline: &dalec.SourceInline{Dir: &dalec.SourceInlineDir{
				Files: map[string]*dalec.SourceInlineFile{"hello": {Contents: "#!/bin/sh\necho hello\n", Permissions: 0o755}},
			}}},
		}
		spec.Tests = []*dalec.TestSpec{{
			Name: "app runs from its own layer",
			Steps: []dalec.TestStep{{
				Command: "/usr/bin/hello",
				Stdout:  dalec.CheckOutput{Contains: []string{"hello"}},
			}},
		}}
		return spec
	}

	t.Run("spec field", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)

		spec := newSpec(t)
		spec.Image = &dalec.ImageConfig{Layers: dalec.ImageLayersPerPackage}

		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
			sr := newSolveRequest(withSpec(ctx, t, &spec), withBuildTarget(target))
			res := solveT(ctx, t, gwc, sr)
			checkGroups(t, &spec, layerGroups(t, res))
		})
	})

	t.Run("build arg", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)

		spec := newSpec(t)

		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
			sr := newSolveRequest(
				withSpec(ctx, t, &spec),
				withBuildTarget(target),
				withBuildArg(dalec.BuildArgDalecImageLayers, dalec.ImageLayersPerPackage),
			)
			res := solveT(ctx, t, gwc, sr)
			checkGroups(t, &spec, layerGroups(t, res))
		})
	})

	t.Run("rebuilt package with identical contents", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)

		// Rebuilding the package gives its files new timestamps but the same
		// contents. The second build must use the new timestamps instead of
		// reusing a cached layer from the first build.
		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
			for _, rev := range []string{"1", "2"} {
				spec := newSpec(t)
				spec.Revision = rev
				spec.Image = &dalec.ImageConfig{Layers: dalec.ImageLayersPerPackage}
				sr := newSolveRequest(withSpec(ctx, t, &spec), withBuildTarget(target))
				res := solveT(ctx, t, gwc, sr)
				checkGroups(t, &spec, layerGroups(t, res))
			}
		})
	})

	t.Run("update previous image", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)

		spec := newSpec(t)
		spec.Image = &dalec.ImageConfig{Layers: dalec.ImageLayersPerPackage}

		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
			pkgRes := solveT(ctx, t, gwc, newSolveRequest(withSpec(ctx, t, &spec), withBuildTarget(testConfig.Target.Package)))
			pkgSt := resultToState(t, pkgRes)

			prevRes := solveT(ctx, t, gwc, newSolveRequest(withSpec(ctx, t, &spec), withBuildTarget(target), withBuildContext(ctx, t, dalec.GenericPkg, pkgSt)))
			prevSt := resultToState(t, prevRes)

			res := solveT(ctx, t, gwc, newSolveRequest(
				withSpec(ctx, t, &spec),
				withBuildTarget(target),
				withBuildContext(ctx, t, dalec.PreviousImageContext, prevSt),
				withBuildContext(ctx, t, dalec.GenericPkg, pkgSt),
			))
			checkGroups(t, &spec, layerGroups(t, res))

			var upgraded bool
			for _, op := range test.LLBOpsFromState(ctx, t, resultToState(t, res)) {
				if op.OpMetadata.ProgressGroup.GetName() == "Update packages" {
					upgraded = true
				}
			}
			assert.Check(t, upgraded, "expected packages to be upgraded in the previous image")
		})
	})

	t.Run("previous image requires per-package layers", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)

		spec := newSpec(t)

		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
			sr := newSolveRequest(
				withSpec(ctx, t, &spec),
				withBuildTarget(target),
				withBuildContext(ctx, t, dalec.PreviousImageContext, notAnImage),
			)
			_, err := gwc.Solve(ctx, sr)
			assert.ErrorContains(t, err, "requires per-package image layers")
		})
	})

	t.Run("previous image without spec package information", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)

		spec := newSpec(t)
		spec.Image = &dalec.ImageConfig{Layers: dalec.ImageLayersPerPackage}

		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
			sr := newSolveRequest(
				withSpec(ctx, t, &spec),
				withBuildTarget(target),
				withBuildContext(ctx, t, dalec.PreviousImageContext, notAnImage),
			)
			_, err := gwc.Solve(ctx, sr)
			assert.ErrorContains(t, err, "cannot tell which packages")
		})
	})

	t.Run("require prebuilt package", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)

		spec := newSpec(t)

		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
			sr := newSolveRequest(
				withSpec(ctx, t, &spec),
				withBuildTarget(target),
				withBuildArg(dalec.BuildArgDalecRequirePrebuiltPkg, "1"),
			)
			_, err := gwc.Solve(ctx, sr)
			assert.ErrorContains(t, err, dalec.BuildArgDalecRequirePrebuiltPkg)
		})
	})
}
