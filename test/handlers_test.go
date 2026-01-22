package test

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/containerd/platforms"
	"github.com/goccy/go-yaml"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests/targets"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/test/testenv"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

// TestHandlerTargetForwarding tests that targets are forwarded to the correct frontend.
// We do this by registering a phony frontend and then forwarding a target to it and checking the outputs.
func TestHandlerTargetForwarding(t *testing.T) {
	t.Parallel()

	runTest := func(t *testing.T, f testenv.TestFunc) {
		t.Helper()
		ctx := startTestSpan(baseCtx, t)
		testEnv.RunTest(ctx, t, f)
	}

	t.Run("list targets", func(t *testing.T) {
		t.Parallel()
		runTest(t, func(ctx context.Context, gwc gwclient.Client) {
			// Make sure phony is not in the list of targets since it shouldn't be registered in the base frontend.
			ls := listTargets(ctx, t, gwc, &dalec.Spec{
				Targets: map[string]dalec.Target{
					// Note: This is not setting the frontend image, so it should use the default frontend.
					"phony": {},
				},
			})

			checkTargetExists(t, ls, "debug/resolve")
			if slices.ContainsFunc(ls.Targets, func(tgt targets.Target) bool {
				return strings.Contains(tgt.Name, "phony")
			}) {
				t.Fatal("found phony target")
			}

			// Now make sure the forwarded target works.
			spec := &dalec.Spec{
				Targets: map[string]dalec.Target{
					"phony": {
						Frontend: &dalec.Frontend{
							Image: phonyRef,
						},
					},
				}}

			// Make sure phony is in the list of targets since it should be registered in the forwarded frontend.
			ls = listTargets(ctx, t, gwc, spec)
			checkTargetExists(t, ls, "debug/resolve")
			checkTargetExists(t, ls, "phony/check")
			checkTargetExists(t, ls, "phony/debug/resolve")
		})
	})

	t.Run("execute target", func(t *testing.T) {
		t.Parallel()
		runTest(t, func(ctx context.Context, gwc gwclient.Client) {
			spec := &dalec.Spec{
				Targets: map[string]dalec.Target{
					"phony": {
						Frontend: &dalec.Frontend{
							Image: phonyRef,
						},
					},
				}}

			sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget("phony/check"))
			res := solveT(ctx, t, gwc, sr)

			dt := readFile(ctx, t, "hello", res)
			expect := []byte("phony hello")
			if !bytes.Equal(dt, expect) {
				t.Fatalf("expected %q, got %q", expect, string(dt))
			}

			// In this case we want to make sure that any targets that are registered by the frontend are namespaced by our target name prefix.
			// This is to ensure that the frontend is not overwriting any other targets.
			// Technically I suppose the target in the user-supplied spec could technically interfere with the base frontend, but that's not really a concern.
			// e.g. if a user-supplied target was called "debug" it could overwrite the "debug/resolve" target in the base frontend.

			sr = newSolveRequest(withSpec(ctx, t, spec), withBuildTarget("debug/resolve"))
			res = solveT(ctx, t, gwc, sr)

			// The builtin debug/resolve target adds the resolved spec to /spec.yml, so check that its there.
			statFile(ctx, t, "spec.yml", res)

			sr = newSolveRequest(withSpec(ctx, t, spec), withBuildTarget("phony/debug/resolve"))
			res = solveT(ctx, t, gwc, sr)

			// The phony/debug/resolve target creates a file with the contents "phony resolve".
			// Check that its there to ensure we got the expected target.
			checkFile(ctx, t, "resolve", res, []byte("phony resolve"))
		})
	})

	t.Run("target not found", func(t *testing.T) {
		t.Parallel()
		runTest(t, func(ctx context.Context, gwc gwclient.Client) {
			spec := &dalec.Spec{
				Targets: map[string]dalec.Target{
					"phony": {
						Frontend: &dalec.Frontend{
							Image: phonyRef,
						},
					},
				},
			}
			sr := newSolveRequest(withBuildTarget("phony/does-not-exist"), withSpec(ctx, t, spec))

			_, err := gwc.Solve(ctx, sr)
			expect := "no such handler for target"
			if err == nil || !strings.Contains(err.Error(), expect) {
				t.Fatalf("expected error %q, got %v", expect, err)
			}
		})
	})
}

func TestHandlerSubrequestResolve(t *testing.T) {
	t.Parallel()
	ctx := startTestSpan(baseCtx, t)

	testPlatforms := func(ctx context.Context, pls ...string) func(t *testing.T) {
		return func(t *testing.T) {
			t.Parallel()
			startTestSpan(ctx, t)

			runTest(t, func(ctx context.Context, gwc gwclient.Client) {
				spec := &dalec.Spec{
					Name:     "foobar",
					Version:  "$VERSION",
					Revision: "$TARGETARCH",
					Args: map[string]string{
						"VERSION":    "0.0.1",
						"TARGETARCH": "",
					},
				}
				req := newSolveRequest(withSpec(ctx, t, spec), withSubrequest("frontend.dalec.resolve"), func(cfg *newSolveRequestConfig) {
					if len(pls) == 0 {
						return
					}
					if cfg.req.FrontendOpt == nil {
						cfg.req.FrontendOpt = make(map[string]string)
					}
					cfg.req.FrontendOpt["platform"] = strings.Join(pls, ",")
				})

				res, err := gwc.Solve(ctx, req)
				assert.NilError(t, err)

				dt, ok := res.Metadata["result.txt"]
				assert.Assert(t, ok)

				var ls []dalec.Spec
				err = yaml.Unmarshal(dt, &ls)
				assert.NilError(t, err)

				var checkPlatforms []ocispecs.Platform

				for _, p := range pls {
					platform, err := platforms.Parse(p)
					assert.NilError(t, err)
					checkPlatforms = append(checkPlatforms, platform)
				}

				if len(checkPlatforms) == 0 {
					// No platform set, so we need to read the platform from the builder
					p := readDefaultPlatform(ctx, t, gwc)
					checkPlatforms = append(checkPlatforms, p)
				}

				assert.Assert(t, cmp.Len(ls, len(checkPlatforms)))

				for i, p := range checkPlatforms {
					s := ls[i]
					assert.Equal(t, s.Name, "foobar")
					assert.Equal(t, s.Version, "0.0.1")
					assert.Equal(t, s.Revision, p.Architecture)
				}
			})
		}
	}

	t.Run("no platform", testPlatforms(ctx))
	t.Run("single platform", testPlatforms(ctx, "linux/amd64"))
	t.Run("multi-platform", testPlatforms(ctx, "linux/amd64", "linux/arm64"))
}
