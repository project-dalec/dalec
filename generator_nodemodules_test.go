package dalec

import (
	"context"
	"strings"
	"testing"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec/internal/test"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

// nodeModInstallCmd builds the LLB for a single-source spec whose only source
// uses the provided nodemod generator, then returns the `npm install` command
// emitted into the generated LLB.
func nodeModInstallCmd(ctx context.Context, t *testing.T, gen *GeneratorNodeMod) string {
	t.Helper()

	spec := &Spec{Sources: map[string]Source{
		"src": {
			Inline: &SourceInline{Dir: &SourceInlineDir{Files: map[string]*SourceInlineFile{
				"package.json": {Contents: "{}"},
			}}},
			Generate: []*SourceGenerator{{NodeMod: gen}},
		},
	}}
	spec.FillDefaults()

	sOpt := SourceOpts{SourceFilter: func() (SourceFilterConfig, error) {
		return SourceFilterConfig{}, nil
	}}

	result := spec.NodeModDeps(sOpt, llb.Scratch())
	st, ok := result["src"]
	assert.Assert(t, ok, "expected a generated node module source")

	for _, op := range test.LLBOpsFromState(ctx, t, st) {
		exec := op.Op.GetExec()
		if exec == nil {
			continue
		}
		cmd := strings.Join(exec.GetMeta().GetArgs(), " ")
		if strings.Contains(cmd, "npm install") {
			return cmd
		}
	}

	t.Fatal("no npm install command found in generated LLB")
	return ""
}

func TestNodeModRegistry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("registry set adds --registry flag", func(t *testing.T) {
		const registry = "https://packagefeedproxy.microsoft.io/npm/"
		cmd := nodeModInstallCmd(ctx, t, &GeneratorNodeMod{Registry: registry})
		assert.Check(t, cmp.Contains(cmd, "--registry="+registry))
	})

	t.Run("registry unset omits --registry flag", func(t *testing.T) {
		cmd := nodeModInstallCmd(ctx, t, &GeneratorNodeMod{})
		assert.Check(t, !strings.Contains(cmd, "--registry"),
			"expected no --registry flag when Registry is unset; got: %s", cmd)
	})
}
