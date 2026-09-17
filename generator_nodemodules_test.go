package dalec

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/moby/buildkit/client/llb"
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
