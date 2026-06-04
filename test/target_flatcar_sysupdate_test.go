package test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/project-dalec/dalec/targets/linux/flatcar"
	"gotest.tools/v3/assert"
)

func TestFlatcarSysupdate(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	platform := ocispecs.Platform{OS: "linux", Architecture: "amd64"}
	spec := newSimpleSpec()
	spec.Name = "flatcar-sysupdate-test"
	spec.Version = "1.2.3"
	spec.Revision = "4"

	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
		res := solveT(ctx, t, gwc, newSolveRequest(
			withBuildTarget(flatcar.TargetKey+"/testing/sysext/sysupdate"),
			withPlatform(platform),
			withSpec(ctx, t, spec),
		))

		const imageName = "flatcar-sysupdate-test-v1.2.3-4-x86-64.raw"
		image := readFile(ctx, t, "/"+imageName, res)
		checksum := strings.TrimSpace(string(readFile(ctx, t, "/SHA256SUMS."+spec.Name, res)))

		wantChecksum := fmt.Sprintf("%x  %s", sha256.Sum256(image), imageName)
		assert.Equal(t, checksum, wantChecksum)
	})
}
