package test

import (
	"context"
	"encoding/json"
	"path/filepath"
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

	t.Run("previous image without a package database", func(t *testing.T) {
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
			assert.ErrorContains(t, err, "has no RPM package database")
		})
	})

	t.Run("update previous image from a newer repository", func(t *testing.T) {
		if testConfig.Worker.TestRepoConfig == nil || testConfig.Worker.CreateRepo == nil {
			t.Skip("no test repository support for this target")
		}
		t.Parallel()
		ctx := startTestSpan(ctx, t)

		const keyName = "public.gpg"
		repoPath := filepath.Join("/opt/repo", createRepoSuffix())
		depName := "dalec-test-ppl-update-dep"

		depSpec := func(version string) *dalec.Spec {
			return &dalec.Spec{
				Name:        depName,
				Version:     version,
				Revision:    "1",
				Description: "Runtime dependency updated from a local repository",
				License:     "MIT",
				Sources: map[string]dalec.Source{
					"dep-version": {Inline: &dalec.SourceInline{File: &dalec.SourceInlineFile{
						Contents:    "#!/bin/sh\necho \"dep " + version + "\"\n",
						Permissions: 0o755,
					}}},
				},
				Artifacts: dalec.Artifacts{Binaries: map[string]dalec.ArtifactConfig{"dep-version": {}}},
			}
		}

		appSpec := func() dalec.Spec {
			spec := newSpec(t)
			spec.Image = &dalec.ImageConfig{Layers: dalec.ImageLayersPerPackage}
			spec.Dependencies.Runtime[depName] = dalec.PackageConstraints{}
			spec.Dependencies.ExtraRepos = []dalec.PackageRepositoryConfig{{
				Config: testConfig.Worker.TestRepoConfig(keyName, repoPath),
				Keys:   map[string]dalec.Source{keyName: {Context: &dalec.SourceContext{Name: "repo-public-key"}, Path: keyName}},
				Data:   []dalec.SourceMount{{Dest: repoPath, Spec: dalec.Source{Context: &dalec.SourceContext{Name: "test-repo"}}}},
				Envs:   []string{"install"},
			}}
			return spec
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
			w := reqToState(ctx, gwc, newSolveRequest(withBuildTarget(testConfig.Target.Worker), withSpec(ctx, t, nil)), t)
			key := generateGPGKey(w, false)
			repo := func(version string, extra ...llb.State) llb.State {
				pkg := reqToState(ctx, gwc, newSolveRequest(withSpec(ctx, t, depSpec(version)), withBuildTarget(testConfig.Target.Package)), t)
				if len(extra) > 0 {
					pkg = llb.Merge(append([]llb.State{pkg}, extra...))
				}
				withRepo := w.With(testConfig.Worker.CreateRepo(pkg, repoPath, testConfig.Worker.SignRepo(key, repoPath)))
				return llb.Scratch().File(llb.Copy(withRepo, repoPath, "/", &llb.CopyInfo{CopyDirContentsOnly: true}))
			}

			readFile := func(res *gwclient.Result, name string) string {
				ref, err := res.SingleRef()
				assert.NilError(t, err)
				dt, err := ref.ReadFile(ctx, gwclient.ReadRequest{Filename: name})
				assert.NilError(t, err)
				return string(dt)
			}

			spec := appSpec()
			prev := solveT(ctx, t, gwc, newSolveRequest(
				withSpec(ctx, t, &spec),
				withBuildTarget(target),
				withBuildContext(ctx, t, "test-repo", repo("1.0.0")),
				withBuildContext(ctx, t, "repo-public-key", key),
			))
			assert.Check(t, cmp.Contains(readFile(prev, "/usr/bin/dep-version"), "dep 1.0.0"))

			// The repository also publishes a newer revision of the spec's own
			// package. The update must keep the spec package from the
			// previous image instead of upgrading it from the repository.
			newer := appSpec()
			newer.Revision = "2"
			// Building the package does not need the test repository.
			newer.Dependencies.ExtraRepos = nil
			newer.Sources["src"].Inline.Dir.Files["hello"] = &dalec.SourceInlineFile{Contents: "#!/bin/sh\necho newer\n", Permissions: 0o755}
			newerPkg := reqToState(ctx, gwc, newSolveRequest(withSpec(ctx, t, &newer), withBuildTarget(testConfig.Target.Package)), t)

			// Updating must not build the spec's package: make building it fail.
			update := appSpec()
			update.Build.Steps = []dalec.BuildStep{{Command: "echo the spec package must not be built when updating an image; exit 1"}}

			res := solveT(ctx, t, gwc, newSolveRequest(
				withSpec(ctx, t, &update),
				withBuildTarget(target),
				withBuildContext(ctx, t, "test-repo", repo("2.0.0", newerPkg)),
				withBuildContext(ctx, t, "repo-public-key", key),
				withBuildContext(ctx, t, dalec.PreviousImageContext, resultToState(t, prev)),
			))

			assert.Check(t, cmp.Contains(readFile(res, "/usr/bin/dep-version"), "dep 2.0.0"), "dependency should be upgraded")
			assert.Check(t, cmp.Equal(readFile(res, "/usr/bin/hello"), readFile(prev, "/usr/bin/hello")), "spec package should be kept, not upgraded from the repository")
			checkGroups(t, &update, layerGroups(t, res))
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
