package dalec

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

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
		"PATH=/bin:/usr/bin",
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
		"PATH=/bin:/usr/bin",
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
		"PATH=/bin:/usr/bin",
		"HTTP_PROXY=http://proxy.example:3128",
		"HTTPS_PROXY=https://proxy.example:8443",
		"DALEC_DISABLE_PROXY_CONFIG=1",
	}

	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, string(out))
	assert.Equal(t, string(out), "proxy=\nhttps_proxy=\nnoproxy=\ncafile=\nextra_ca=\n")
}
