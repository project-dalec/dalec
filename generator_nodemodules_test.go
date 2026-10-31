package dalec

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	"github.com/project-dalec/dalec/internal/test"
	"gotest.tools/v3/assert"
)

func TestNodeModRegistry(t *testing.T) {
	t.Parallel()

	t.Run("registry is shell-quoted", func(t *testing.T) {
		t.Parallel()
		const registry = "https://example.test/npm/?a=1&b=$c'quoted"
		args := nodeModInstallArgs(t.Context(), t, &GeneratorNodeMod{Registry: registry})
		assert.Assert(t, strings.Contains(strings.Join(args, " "), "--registry='https://example.test/npm/?a=1&b=$c'\"'\"'quoted'"), args)
	})

	t.Run("registry unset omits --registry flag", func(t *testing.T) {
		t.Parallel()
		args := nodeModInstallArgs(t.Context(), t, &GeneratorNodeMod{})
		for _, a := range args {
			assert.Assert(t, !strings.Contains(a, "--registry="),
				"expected no --registry flag when Registry is unset; got: %v", args)
		}
	})
}

// TestNodeModDepsUnpatchedSource checks that npm installs from the patched
// source but the returned source does not carry the patches itself, since
// targets apply the spec patches to the returned state again.
func TestNodeModDepsUnpatchedSource(t *testing.T) {
	t.Parallel()

	spec := &Spec{
		Sources: map[string]Source{
			"src": {
				Inline: &SourceInline{Dir: &SourceInlineDir{Files: map[string]*SourceInlineFile{
					"package.json": {Contents: "{}"},
				}}},
				Generate: []*SourceGenerator{{NodeMod: &GeneratorNodeMod{}}},
			},
			"fix.patch": {
				Inline: &SourceInline{File: &SourceInlineFile{Contents: "patch contents"}},
			},
		},
		Patches: map[string][]PatchSpec{
			"src": {{Source: "fix.patch"}},
		},
	}
	spec.FillDefaults()

	sOpt := SourceOpts{SourceFilter: func() (SourceFilterConfig, error) {
		return SourceFilterConfig{}, nil
	}}

	st, ok := spec.NodeModDeps(sOpt, llb.Scratch())["src"]
	assert.Assert(t, ok, "expected a generated node module source")

	def, err := st.Marshal(t.Context())
	assert.NilError(t, err)

	ops := make(map[digest.Digest]*pb.Op, len(def.Def))
	for _, dt := range def.Def {
		var op pb.Op
		assert.NilError(t, op.UnmarshalVT(dt))
		ops[digest.FromBytes(dt)] = &op
	}

	execContains := func(op *pb.Op, s string) bool {
		exec := op.GetExec()
		return exec != nil && strings.Contains(strings.Join(exec.GetMeta().GetArgs(), " "), s)
	}

	// reachesPatch reports whether a patch exec can be reached from dgst
	// without passing through the npm install exec, unless it is the start.
	var reachesPatch func(dgst digest.Digest, start bool) bool
	reachesPatch = func(dgst digest.Digest, start bool) bool {
		op := ops[dgst]
		if execContains(op, "patch -p") {
			return true
		}
		if !start && execContains(op, "npm install") {
			return false
		}
		for _, inp := range op.Inputs {
			if reachesPatch(digest.Digest(inp.Digest), false) {
				return true
			}
		}
		return false
	}

	// The last op is the terminal one which has the final state as input.
	var terminal pb.Op
	assert.NilError(t, terminal.UnmarshalVT(def.Def[len(def.Def)-1]))
	assert.Assert(t, !reachesPatch(digest.Digest(terminal.Inputs[0].Digest), false),
		"the returned source must not have the patches applied")

	var npmInstalls int
	for dgst, op := range ops {
		if !execContains(op, "npm install") {
			continue
		}
		npmInstalls++
		assert.Assert(t, reachesPatch(dgst, true), "npm install must run against the patched source")
	}
	assert.Equal(t, npmInstalls, 1)
}

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

func TestConfigureNpmProxySetsProxyConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}

	cmd := exec.Command("/bin/sh", "-c", npmProxyConfigScript+`
configure_npm_proxy
	printf 'proxy=%s\nhttps_proxy=%s\nnoproxy=%s\ncafile=%s\nextra_ca=%s\n' \
	"${npm_config_proxy:-}" \
	"${npm_config_https_proxy:-}" \
	"${npm_config_noproxy:-}" \
	"${npm_config_cafile:-}" \
	"${NODE_EXTRA_CA_CERTS:-}"
`)
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HTTP_PROXY=http://proxy.example:3128",
		"HTTPS_PROXY=https://proxy.example:8443",
		"NO_PROXY=localhost,127.0.0.1",
	}

	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, string(out))

	config := string(out)
	assert.Assert(t, strings.Contains(config, "proxy=http://proxy.example:3128\n"), config)
	assert.Assert(t, strings.Contains(config, "https_proxy=https://proxy.example:8443\n"), config)
	assert.Assert(t, strings.Contains(config, "noproxy=localhost,127.0.0.1\n"), config)
	assert.Assert(t, strings.Contains(config, "cafile=/"), config)
	assert.Assert(t, strings.Contains(config, "extra_ca=/"), config)
}

func TestConfigureNpmProxyDoesNotTraceProxyValues(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}

	cmd := exec.Command("/bin/sh", "-c", "set -x\n"+npmProxyConfigScript+"\nconfigure_npm_proxy\n")
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HTTP_PROXY=http://user:secret@proxy.example:3128",
	}

	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, string(out))
	assert.Assert(t, !strings.Contains(string(out), "secret"), string(out))
}

func TestConfigureNpmProxyDisabled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}

	cmd := exec.Command("/bin/sh", "-c", npmProxyConfigScript+`
configure_npm_proxy
	printf 'proxy=%s\nhttps_proxy=%s\nnoproxy=%s\ncafile=%s\nextra_ca=%s\n' \
	"${npm_config_proxy:-}" \
	"${npm_config_https_proxy:-}" \
	"${npm_config_noproxy:-}" \
	"${npm_config_cafile:-}" \
	"${NODE_EXTRA_CA_CERTS:-}"
`)
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HTTP_PROXY=http://proxy.example:3128",
		"HTTPS_PROXY=https://proxy.example:8443",
		"DALEC_DISABLE_PROXY_CONFIG=1",
	}

	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, string(out))
	assert.Equal(t, string(out), "proxy=\nhttps_proxy=\nnoproxy=\ncafile=\nextra_ca=\n")
}
