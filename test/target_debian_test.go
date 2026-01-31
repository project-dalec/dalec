package test

import (
	"context"
	"testing"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/targets/linux/deb/debian"
)

func TestDalecTargetTrixie(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	testConf := debLinuxTestConfigFor(
		debian.TrixieDefaultTargetKey,
		debian.TrixieConfig,
		withPackageOverride("rust", "rust-all"),
		withPackageOverride("bazel", "bazel-bootstrap"),
	)

	testLinuxDistro(ctx, t, testConf)
	testDebianBaseDependencies(t, testConf.Target)
}

func TestDalecTargetBookworm(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	testConf := debLinuxTestConfigFor(
		debian.BookwormDefaultTargetKey,
		debian.BookwormConfig,
		withPackageOverride("rust", "rust-all"),
		withPackageOverride("bazel", "bazel-bootstrap"),
	)

	testLinuxDistro(ctx, t, testConf)
	testDebianBaseDependencies(t, testConf.Target)
}

func TestDalecTargetBullseye(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	testConf := debLinuxTestConfigFor(
		debian.BullseyeDefaultTargetKey,
		debian.BullseyeConfig,
		withPackageOverride("golang", "golang-1.19"),
		withPackageOverride("rust", "cargo-web"),
		withPackageOverride("bazel", noPackageAvailable),
	)

	testLinuxDistro(ctx, t, testConf)
	testDebianBaseDependencies(t, testConf.Target)
}

func testDebianBaseDependencies(t *testing.T, target targetConfig) {
	t.Run("base deps", func(t *testing.T) {
		t.Parallel()

		ctx := startTestSpan(baseCtx, t)
		spec := newSimpleSpec()
		spec.Tests = []*dalec.TestSpec{
			{
				Files: map[string]dalec.FileCheckOutput{
					"/etc/ssl/certs": {
						Permissions: 0755,
						IsDir:       true,
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			req := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(target.Container))
			solveT(ctx, t, client, req)
		})
	})
}
