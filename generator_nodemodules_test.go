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

// nodeModInstallArgs builds the LLB for a single-source spec whose only source
// uses the provided nodemod generator, then returns the argv of the `npm
// install` exec emitted into the generated LLB.
func nodeModInstallArgs(ctx context.Context, t *testing.T, gen *GeneratorNodeMod) []string {
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
		args := exec.GetMeta().GetArgs()
		if strings.Contains(strings.Join(args, " "), "npm install") {
			return args
		}
	}

	t.Fatal("no npm install command found in generated LLB")
	return nil
}

func TestNodeModRegistry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("registry is passed as a single literal arg without a shell", func(t *testing.T) {
		// URL deliberately contains shell metacharacters (& and $). If the
		// command were built with sh -c, these would be interpreted by the
		// shell and truncate the value or run unintended commands. As an argv
		// element the URL must survive verbatim.
		const registry = "https://example.test/npm/?a=1&b=$c"
		args := nodeModInstallArgs(ctx, t, &GeneratorNodeMod{Registry: registry})
		assert.Check(t, cmp.Contains(args, "--registry="+registry))
		assert.Check(t, args[0] != "sh" && args[0] != "bash",
			"expected npm to be invoked directly without a shell; got: %v", args)
	})

	t.Run("registry unset omits --registry flag", func(t *testing.T) {
		args := nodeModInstallArgs(ctx, t, &GeneratorNodeMod{})
		for _, a := range args {
			assert.Check(t, !strings.HasPrefix(a, "--registry"),
				"expected no --registry flag when Registry is unset; got: %v", args)
		}
	})
}
