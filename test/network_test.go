package test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	moby_buildkit_v1_frontend "github.com/moby/buildkit/frontend/gateway/pb"
	"github.com/project-dalec/dalec"
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
					t.Fatalf("expected exit error, got %T: %v", errors.Unwrap(err), err)
				}
			})
		})
	}
}
