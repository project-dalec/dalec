package test

import (
	"context"
	"testing"

	"github.com/containerd/platforms"
	"github.com/goccy/go-yaml"
	"github.com/google/go-cmp/cmp"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/test/testenv"
)

func getBuildPlatform(ctx context.Context, t *testing.T) *ocispecs.Platform {
	buildPlatform := make(chan *ocispecs.Platform, 1)
	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
		defer close(buildPlatform)
		dc, err := dockerui.NewClient(gwc)
		if err != nil {
			t.Fatal(err)
		}
		if len(dc.BuildPlatforms) > 1 {
			t.Fatal("unexpected build platforms")
		}
		buildPlatform <- &dc.BuildPlatforms[0]
	})

	p := <-buildPlatform

	if p == nil {
		t.Fatal("build platform not found")
	}

	return p
}

func TestPassthroughVars(t *testing.T) {
	t.Parallel()

	runTest := func(t *testing.T, f testenv.TestFunc) {
		t.Helper()
		ctx := startTestSpan(baseCtx, t)
		testEnv.RunTest(ctx, t, f)
	}

	ctx := startTestSpan(baseCtx, t)
	var buildPlatform = getBuildPlatform(ctx, t)

	tests := []struct {
		name               string
		needsBuildPlatform bool
		targetPlatform     ocispecs.Platform
		buildPlatform      *ocispecs.Platform
		optInArgs          map[string]string
		env                map[string]string
		wantEnv            map[string]string
	}{
		{
			name:           "target linux/amd64",
			targetPlatform: platforms.MustParse("linux/amd64"),
			optInArgs: map[string]string{
				"TARGETOS":       "",
				"TARGETARCH":     "",
				"TARGETVARIANT":  "",
				"TARGETPLATFORM": "",
			},
			env: map[string]string{
				"TARGETOS":       "${TARGETOS}",
				"TARGETARCH":     "${TARGETARCH}",
				"TARGETVARIANT":  "${TARGETVARIANT}",
				"TARGETPLATFORM": "${TARGETPLATFORM}",
			},
			wantEnv: map[string]string{
				"TARGETOS":       "linux",
				"TARGETARCH":     "amd64",
				"TARGETVARIANT":  "",
				"TARGETPLATFORM": "linux/amd64",
			},
		},
		{
			name:           "target linux/arm/v5",
			targetPlatform: platforms.MustParse("linux/arm/v5"),
			optInArgs: map[string]string{
				"TARGETOS":       "",
				"TARGETARCH":     "",
				"TARGETVARIANT":  "",
				"TARGETPLATFORM": "",
			},
			env: map[string]string{
				"TARGETOS":       "${TARGETOS}",
				"TARGETARCH":     "${TARGETARCH}",
				"TARGETVARIANT":  "${TARGETVARIANT}",
				"TARGETPLATFORM": "${TARGETPLATFORM}",
			},
			wantEnv: map[string]string{
				"TARGETOS":       "linux",
				"TARGETARCH":     "arm",
				"TARGETVARIANT":  "v5",
				"TARGETPLATFORM": "linux/arm/v5",
			},
		},
		{
			name:           "build platform",
			targetPlatform: platforms.DefaultSpec(),
			optInArgs: map[string]string{
				"BUILDOS":       "",
				"BUILDARCH":     "",
				"BUILDVARIANT":  "",
				"BUILDPLATFORM": "",
			},
			env: map[string]string{
				"BUILDOS":       "${BUILDOS}",
				"BUILDARCH":     "${BUILDARCH}",
				"BUILDVARIANT":  "${BUILDVARIANT}",
				"BUILDPLATFORM": "${BUILDPLATFORM}",
			},
			wantEnv: map[string]string{
				"BUILDOS":       buildPlatform.OS,
				"BUILDARCH":     buildPlatform.Architecture,
				"BUILDVARIANT":  buildPlatform.Variant,
				"BUILDPLATFORM": platforms.Format(*buildPlatform),
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			runTest(t, func(ctx context.Context, gwc gwclient.Client) {
				spec := &dalec.Spec{Args: tt.optInArgs, Build: dalec.ArtifactBuild{Env: tt.env, Steps: []dalec.BuildStep{{Command: "echo hello"}}}}
				req := newSolveRequest(withBuildTarget("debug/resolve"), withSpec(ctx, t, spec), withPlatform(tt.targetPlatform))

				res := solveT(ctx, t, gwc, req)
				specBytes := readFile(ctx, t, "spec.yml", res)

				var resolvedSpec dalec.Spec
				err := yaml.Unmarshal(specBytes, &resolvedSpec)
				if err != nil {
					t.Fatal(err)
				}

				if !cmp.Equal(tt.wantEnv, resolvedSpec.Build.Env) {
					t.Fatalf("resolved environment does not match: %v", cmp.Diff(tt.wantEnv, resolvedSpec.Build.Env))
				}
			})
		})

	}
}
