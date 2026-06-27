package test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/util/stack"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
	"github.com/project-dalec/dalec/targets/linux/flatcar"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestFlatcar(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)

	t.Run("sysext", func(t *testing.T) {
		t.Parallel()

		ctx := startTestSpan(ctx, t)
		testFlatcarSysext(ctx, t, nil,
			[]string{
				"ID=flatcar\n",
				"ARCHITECTURE=x86-64\n",
				"EXTENSION_RELOAD_MANAGER=1\n",
				"SYSEXT_LEVEL=1.0\n",
			},
			[]string{
				"VERSION_ID=",
			},
		)
	})

	t.Run("sysext version id", func(t *testing.T) {
		t.Parallel()

		ctx := startTestSpan(ctx, t)
		testFlatcarSysext(ctx, t,
			map[string]string{
				"DALEC_SYSEXT_OS_VERSION_ID": "4593.0.0",
			},
			[]string{
				"ID=flatcar\n",
				"ARCHITECTURE=x86-64\n",
				"EXTENSION_RELOAD_MANAGER=1\n",
				"VERSION_ID=4593.0.0\n",
			},
			[]string{
				"SYSEXT_LEVEL=",
			},
		)
	})
}

func testFlatcarSysext(ctx context.Context, t *testing.T, buildArgs map[string]string, want, notWant []string) {
	t.Helper()

	platform := ocispecs.Platform{OS: "linux", Architecture: "amd64"}
	spec := newSimpleSpec()
	spec.Name = "flatcar-hello"

	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
		opts := []srOpt{
			withBuildTarget(flatcar.TargetKey + "/testing/sysext"),
			withPlatform(platform),
			withSpec(ctx, t, spec),
		}

		for k, v := range buildArgs {
			opts = append(opts, withBuildArg(k, v))
		}

		res := solveT(ctx, t, gwc, newSolveRequest(opts...))

		ref, err := res.SingleRef()
		if err != nil {
			t.Fatal(err)
		}

		expectedPath := "/" + spec.Name + ".raw"
		if _, err := ref.StatFile(ctx, gwclient.StatRequest{Path: expectedPath}); err != nil {
			t.Fatalf("expected Flatcar sysext image not found: %v", err)
		}

		extracted := extractFlatcarSysext(ctx, t, gwc, ref, expectedPath, platform)
		extractedRef, err := extracted.SingleRef()
		if err != nil {
			t.Fatal(err)
		}

		if _, err := extractedRef.StatFile(ctx, gwclient.StatRequest{Path: "/usr/bin/foo"}); err != nil {
			t.Fatalf("expected binary in Flatcar sysext not found: %v", err)
		}

		metadataPath := fmt.Sprintf("/usr/lib/extension-release.d/extension-release.%s", spec.Name)
		metadata := string(readFile(ctx, t, metadataPath, extracted))

		for _, expected := range want {
			assert.Check(t, cmp.Contains(metadata, expected), "expected metadata to contain %q:\n%s", expected, metadata)
		}
		for _, unexpected := range notWant {
			assert.Check(t, !strings.Contains(metadata, unexpected), "expected metadata not to contain %q:\n%s", unexpected, metadata)
		}
	})
}

func extractFlatcarSysext(ctx context.Context, t *testing.T, gwc gwclient.Client, ref gwclient.Reference, expectedPath string, platform ocispecs.Platform) *gwclient.Result {
	t.Helper()

	sOpt, err := frontend.SourceOptFromClient(ctx, gwc, &platform)
	assert.NilError(t, err)

	pc := dalec.Platform(&platform)
	state, err := ref.ToState()
	if err != nil {
		t.Fatal(err)
	}

	output := flatcar.DefaultConfig.SysextWorker(sOpt, pc).Run(
		llb.Args([]string{"fsck.erofs", "--extract=/output", "/input" + expectedPath}),
		llb.AddMount("/input", state, llb.Readonly),
		dalec.WithConstraints(pc),
	).AddMount("/output", llb.Scratch())

	def, err := output.Marshal(ctx, pc)
	if err != nil {
		t.Fatalf("error marshalling Flatcar sysext extraction: %v", err)
	}

	res, err := gwc.Solve(ctx, gwclient.SolveRequest{
		Definition: def.ToPB(),
		Evaluate:   true,
	})
	if err != nil {
		t.Fatalf("error extracting Flatcar sysext: %+v", stack.Formatter(err))
	}

	extractedRef, err := res.SingleRef()
	if err != nil {
		t.Fatal(err)
	}
	if err := extractedRef.Evaluate(ctx); err != nil {
		t.Fatalf("error evaluating extracted Flatcar sysext: %+v", stack.Formatter(err))
	}

	return res
}
