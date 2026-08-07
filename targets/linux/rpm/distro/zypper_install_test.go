package distro

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

// TestZypperInstallTemplateRenders ensures the inline install-script template
// parses and executes across the combinations of includeDocs and the
// container-assembly root path.
func TestZypperInstallTemplateRenders(t *testing.T) {
	for _, includeDocs := range []bool{true, false} {
		for _, root := range []string{"", "/tmp/rootfs"} {
			cfg := &dnfInstallConfig{includeDocs: includeDocs, root: root}
			assert.Assert(t, ZypperInstall(cfg, "", []string{"pkg"}) != nil)
		}
	}
}

func TestConfigureZypperProxyInstallsTemporaryBuildKitCA(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}

	dir := t.TempDir()
	sourceBundle := filepath.Join(dir, "source.pem")
	proxyAnchor := filepath.Join(dir, "anchors", "proxy.pem")
	updateLog := filepath.Join(dir, "update.log")
	updateCA := filepath.Join(dir, "update-ca-certificates")
	err := os.WriteFile(sourceBundle, []byte(`system ca
# buildkit proxy CA begin
proxy ca
# buildkit proxy CA end
`), 0o600)
	assert.NilError(t, err)
	err = os.WriteFile(updateCA, []byte("#!/bin/sh\nprintf 'updated\\n' >> \"$DALEC_ZYPPER_UPDATE_LOG\"\n"), 0o700)
	assert.NilError(t, err)

	cmd := exec.Command("/bin/bash", "-c", zypperProxyConfigScript+`
configure_zypper_proxy
grep -q 'buildkit proxy CA begin' "${DALEC_ZYPPER_PROXY_ANCHOR}"
cleanup_zypper_proxy
if [ -e "${DALEC_ZYPPER_PROXY_ANCHOR}" ]; then exit 1; fi
if [ "$(wc -l < "${DALEC_ZYPPER_UPDATE_LOG}")" -ne 2 ]; then exit 1; fi
`)
	cmd.Env = []string{
		"PATH=/bin:/usr/bin",
		"HTTPS_PROXY=http://proxy.example:3128",
		"DALEC_RPM_PROXY_CA_BUNDLE=" + sourceBundle,
		"DALEC_ZYPPER_PROXY_ANCHOR=" + proxyAnchor,
		"DALEC_ZYPPER_UPDATE_CA_CERTIFICATES=" + updateCA,
		"DALEC_ZYPPER_UPDATE_LOG=" + updateLog,
	}

	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, string(out))
}

func TestConfigureZypperProxyDoesNotTraceProxyValues(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}

	caBundle := filepath.Join(t.TempDir(), "ca-bundle.pem")
	err := os.WriteFile(caBundle, []byte("test ca"), 0o600)
	assert.NilError(t, err)

	const proxyURL = "http://dalec-proxy-value-sentinel@proxy.example:3128"
	cmd := exec.Command("/bin/bash", "-c", "set -x\n"+zypperProxyConfigScript+`
configure_zypper_proxy
`)
	cmd.Env = []string{
		"PATH=/bin:/usr/bin",
		"HTTP_PROXY=" + proxyURL,
		"DALEC_RPM_PROXY_CA_BUNDLE=" + caBundle,
	}

	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, string(out))
	assert.Assert(t, !strings.Contains(string(out), proxyURL), string(out))
}

func TestConfigureZypperProxyDisabled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}

	caBundle := filepath.Join(t.TempDir(), "ca-bundle.pem")
	err := os.WriteFile(caBundle, []byte("test ca"), 0o600)
	assert.NilError(t, err)

	cmd := exec.Command("/bin/bash", "-c", zypperProxyConfigScript+`
configure_zypper_proxy
if [ -e "${DALEC_ZYPPER_PROXY_ANCHOR}" ]; then exit 1; fi
`)
	cmd.Env = []string{
		"PATH=/bin:/usr/bin",
		"HTTP_PROXY=http://proxy.example:3128",
		"DALEC_RPM_PROXY_CA_BUNDLE=" + caBundle,
		"DALEC_ZYPPER_PROXY_ANCHOR=" + filepath.Join(t.TempDir(), "proxy.pem"),
		"DALEC_DISABLE_PROXY_CONFIG=1",
	}

	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, string(out))
}
