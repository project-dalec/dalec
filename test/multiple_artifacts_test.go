package test

import (
	"context"
	"strings"
	"testing"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/project-dalec/dalec"
)

// testEmptyArtifacts tests that a package with no artifacts defined in spec.artifacts builds and tests successfully.
func testEmptyArtifacts(ctx context.Context, t *testing.T, targetCfg targetConfig) {
	spec := &dalec.Spec{
		Name:        "test-dalec-empty-artifacts",
		Version:     "0.0.1",
		Revision:    "1",
		Description: "Testing empty artifacts",
		License:     "MIT",
		Targets:     map[string]dalec.Target{},
		Artifacts:   dalec.Artifacts{},
		Build: dalec.ArtifactBuild{
			Steps: []dalec.BuildStep{
				{
					Command: "echo 'hello world' > hello.txt",
				},
			},
		},
	}

	t.Run("primary", func(t *testing.T) {
		t.Parallel()
		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
			sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(targetCfg.Container))
			solveT(ctx, t, gwc, sr)
		})
	})
}

// testArtifactsAtSpecLevel tests that artifacts defined in spec.artifacts are built and tested.
func testArtifactsAtSpecLevel(ctx context.Context, t *testing.T, targetCfg targetConfig) {
	vals := strings.Split(targetCfg.Container, "/")
	primaryTarget := vals[0]

	spec := &dalec.Spec{
		Name:        "test-dalec-single-artifact",
		Version:     "0.0.1",
		Revision:    "1",
		Description: "Testing single artifact",
		License:     "MIT",
		Targets:     map[string]dalec.Target{},
		Artifacts: dalec.Artifacts{
			Binaries: map[string]dalec.ArtifactConfig{
				"hello.txt": {},
			},
		},
		Tests: []*dalec.TestSpec{
			{
				Name: "test hello world",
				Files: map[string]dalec.FileCheckOutput{
					"/usr/bin/hello.txt": {},
					"/usr/bin/readme.md": {
						NotExist: true,
					},
				},
			},
		},
		Build: dalec.ArtifactBuild{
			Steps: []dalec.BuildStep{
				{
					Command: "echo 'hello world' > hello.txt",
				},
			},
		},
	}

	t.Run("test spec level artifacts and no target level artifacts", func(t *testing.T) {
		t.Parallel()
		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
			sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(primaryTarget))
			solveT(ctx, t, gwc, sr)
		})
	})
}

// testMultipleArtifacts tests that a package with multiple artifacts defined in spec.target takes precedent over spec.artifacts
func testTargetArtifactsTakePrecedence(ctx context.Context, t *testing.T, targetCfg targetConfig) {
	// get the target we want to test for
	vals := strings.Split(targetCfg.Container, "/")
	primaryTarget := vals[0]
	// prevent primaryTarget from being the same as secondaryTarget or tertiaryTarget
	secondaryTarget := "mariner2"
	if primaryTarget == secondaryTarget {
		secondaryTarget = "azlinux3"
	}
	tertiaryTarget := "bookworm"
	if primaryTarget == "bookworm" {
		tertiaryTarget = "jammy"
	}

	spec := &dalec.Spec{
		Name:        "test-dalec-multiple-artifacts",
		Version:     "0.0.1",
		Revision:    "1",
		Description: "Testing multiple artifacts",
		License:     "MIT",
		Targets: map[string]dalec.Target{
			primaryTarget: {
				Artifacts: &dalec.Artifacts{
					Binaries: map[string]dalec.ArtifactConfig{
						"from-" + primaryTarget: {},
					},
				},
				Tests: []*dalec.TestSpec{
					{
						Name: "test1",
						Files: map[string]dalec.FileCheckOutput{
							"/usr/bin/from-" + primaryTarget: {
								CheckOutput: dalec.CheckOutput{
									Equals: "hello world\n",
								},
							},
							"/usr/bin/from-" + secondaryTarget: {
								NotExist: true,
							},
							"/usr/bin/from-" + tertiaryTarget: {
								NotExist: true,
							},
							"/usr/bin/from-main": {
								NotExist: true,
							},
						},
					},
				},
			},
			secondaryTarget: {
				Artifacts: &dalec.Artifacts{
					Binaries: map[string]dalec.ArtifactConfig{
						"from-" + secondaryTarget: {},
					},
				},
			},
			tertiaryTarget: {
				Artifacts: &dalec.Artifacts{
					Binaries: map[string]dalec.ArtifactConfig{
						"from-" + tertiaryTarget: {},
					},
				},
			},
		},
		Artifacts: dalec.Artifacts{
			Binaries: map[string]dalec.ArtifactConfig{
				"from-main": {},
			},
		},
		Build: dalec.ArtifactBuild{
			Steps: []dalec.BuildStep{
				{
					Command: "echo 'hello world' > from-" + primaryTarget,
				},
				{
					Command: "echo 'hello world' > from-" + secondaryTarget,
				},
				{
					Command: "echo 'hello world' > from-" + tertiaryTarget,
				},
				{
					Command: "echo 'hello world' > from-main",
				},
			},
		},
	}

	t.Run(primaryTarget, func(t *testing.T) {
		t.Parallel()
		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
			sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(primaryTarget))
			solveT(ctx, t, gwc, sr)
		})
	})
}
