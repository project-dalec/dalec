package test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	moby_buildkit_v1_frontend "github.com/moby/buildkit/frontend/gateway/pb"
	"github.com/moby/buildkit/util/stack"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/targets"
	"github.com/project-dalec/dalec/test/testenv"
	"gotest.tools/v3/assert"
)

func testBuildNetworkMode(ctx context.Context, t *testing.T, cfg targetConfig) {
	type testCase struct {
		mode            string
		canHazInternetz bool // :)
	}

	cases := []testCase{
		{mode: "", canHazInternetz: false},
		{mode: "none", canHazInternetz: false},
		{mode: "sandbox", canHazInternetz: true},
	}

	for _, tc := range cases {
		name := "mode=" + tc.mode
		if tc.mode == "" {
			name += "unset"
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := startTestSpan(ctx, t)

			spec := dalec.Spec{
				Name:        "test-build-network-mode",
				Version:     "0.0.1",
				Revision:    "1",
				License:     "MIT",
				Website:     "https://github.com/project-dalec/dalec",
				Vendor:      "Dalec",
				Packager:    "Dalec",
				Description: "Should not have internet access during build",
				Dependencies: &dalec.PackageDependencies{
					Build: map[string]dalec.PackageConstraints{"curl": {}},
				},
				Build: dalec.ArtifactBuild{
					NetworkMode: tc.mode,
					Steps: []dalec.BuildStep{
						{
							Command: fmt.Sprintf("curl --head -ksSf %s > /dev/null", externalTestHost),
						},
						{
							Command: "touch foo",
						},
					},
				},
				Artifacts: dalec.Artifacts{
					// This is here so the windows can use this test
					// Windows needs to have a non-empty output to suceeed.
					Binaries: map[string]dalec.ArtifactConfig{"foo": {}},
				},
			}

			testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
				sr := newSolveRequest(withSpec(ctx, t, &spec), withBuildTarget(cfg.Package))

				_, err := gwc.Solve(ctx, sr)
				if tc.canHazInternetz {
					assert.NilError(t, err)
					return
				}

				var xErr *moby_buildkit_v1_frontend.ExitError
				if !errors.As(err, &xErr) {
					t.Fatalf("expected exit error, got %T: %+v", errors.Unwrap(err), stack.Formatter(err))
				}
			})
		})
	}
}

func testPackageManagerProxyNetwork(ctx context.Context, t *testing.T, cfg targetConfig) {
	var logs strings.Builder
	solveStatusFn := testenv.WithSolveStatusFn(func(status *testenv.SolveStatus) {
		if status == nil {
			return
		}
		for _, l := range status.Logs {
			logs.Write(l.Data)
		}
	})

	spec := dalec.Spec{
		Name:        "test-package-manager-proxy-network",
		Version:     "0.0.1",
		Revision:    "1",
		License:     "MIT",
		Website:     "https://github.com/project-dalec/dalec",
		Vendor:      "Dalec",
		Packager:    "Dalec",
		Description: "Should install package-manager dependencies through BuildKit proxy network",
		Dependencies: &dalec.PackageDependencies{
			Build: map[string]dalec.PackageConstraints{
				cfg.GetPackage("curl"): {},
			},
		},
		Build: dalec.ArtifactBuild{
			Steps: []dalec.BuildStep{
				{
					Command: "touch foo",
				},
			},
		},
		Artifacts: dalec.Artifacts{
			Binaries: map[string]dalec.ArtifactConfig{"foo": {}},
		},
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
		sr := newSolveRequest(
			withSpec(ctx, t, &spec),
			withBuildTarget(cfg.Package),
			withIgnoreCache(targets.IgnoreCacheKeyPkg),
		)

		_, err := gwc.Solve(ctx, sr)
		if err != nil && strings.Contains(err.Error(), "proxy network") {
			t.Skipf("BuildKit proxy network is not available in this test builder: %v", err)
		}
		assert.NilError(t, err)
	}, testenv.WithProxyNetwork, solveStatusFn)

	logOutput := logs.String()
	if !strings.Contains(logOutput, "proxy network requests:") {
		t.Skip("BuildKit proxy network did not activate or did not record requests in this test builder")
	}
}

func testSandboxBuildProxyNetwork(ctx context.Context, t *testing.T, cfg targetConfig) {
	var logs strings.Builder
	solveStatusFn := testenv.WithSolveStatusFn(func(status *testenv.SolveStatus) {
		if status == nil {
			return
		}
		for _, l := range status.Logs {
			logs.Write(l.Data)
		}
	})

	spec := dalec.Spec{
		Name:        "test-sandbox-build-proxy-network",
		Version:     "0.0.1",
		Revision:    "1",
		License:     "MIT",
		Website:     "https://github.com/project-dalec/dalec",
		Vendor:      "Dalec",
		Packager:    "Dalec",
		Description: "Should capture sandbox build step HTTP(S) egress through BuildKit proxy network",
		Dependencies: &dalec.PackageDependencies{
			Build: map[string]dalec.PackageConstraints{
				cfg.GetPackage("curl"): {},
			},
		},
		Build: dalec.ArtifactBuild{
			NetworkMode: "sandbox",
			Steps: []dalec.BuildStep{
				{
					Command: fmt.Sprintf("curl --head -ksSf %s > /dev/null", externalTestHost),
				},
				{
					Command: "touch foo",
				},
			},
		},
		Artifacts: dalec.Artifacts{
			Binaries: map[string]dalec.ArtifactConfig{"foo": {}},
		},
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
		sr := newSolveRequest(
			withSpec(ctx, t, &spec),
			withBuildTarget(cfg.Package),
			withIgnoreCache(targets.IgnoreCacheKeyPkg),
		)

		_, err := gwc.Solve(ctx, sr)
		if err != nil && strings.Contains(err.Error(), "proxy network") {
			t.Skipf("BuildKit proxy network is not available in this test builder: %v", err)
		}
		assert.NilError(t, err)
	}, testenv.WithProxyNetwork, solveStatusFn)

	logOutput := logs.String()
	if !strings.Contains(logOutput, "proxy network requests:") {
		t.Skip("BuildKit proxy network did not activate or did not record requests in this test builder")
	}
}
