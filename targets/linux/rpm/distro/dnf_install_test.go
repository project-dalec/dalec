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

func TestConfigureDnfProxyAddsCACertOptions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}

	caBundle := filepath.Join(t.TempDir(), "ca-bundle.pem")
	err := os.WriteFile(caBundle, []byte("test ca"), 0o600)
	assert.NilError(t, err)

	cmd := exec.Command("/bin/bash", "-c", dnfProxyConfigScript+`
install_flags="-y"
configure_dnf_proxy
printf '%s\n%s\n%s\n' "${install_flags}" "${SSL_CERT_FILE:-}" "${CURL_CA_BUNDLE:-}"
`)
	cmd.Env = []string{
		"PATH=/bin:/usr/bin",
		"HTTPS_PROXY=http://proxy.example:3128",
		"DALEC_RPM_PROXY_CA_BUNDLE=" + caBundle,
	}

	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, string(out))

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	assert.Equal(t, len(lines), 3, string(out))
	assert.Assert(t, strings.Contains(lines[0], "--setopt=sslverify=1"), lines[0])
	assert.Assert(t, strings.Contains(lines[0], "--setopt=sslcacert="+caBundle), lines[0])
	assert.Equal(t, lines[1], caBundle)
	assert.Equal(t, lines[2], caBundle)
}

func TestConfigureDnfProxyDoesNotTraceProxyValues(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}

	caBundle := filepath.Join(t.TempDir(), "ca-bundle.pem")
	err := os.WriteFile(caBundle, []byte("test ca"), 0o600)
	assert.NilError(t, err)

	cmd := exec.Command("/bin/bash", "-c", "set -x\n"+dnfProxyConfigScript+`
install_flags="-y"
configure_dnf_proxy
`)
	cmd.Env = []string{
		"PATH=/bin:/usr/bin",
		"HTTP_PROXY=http://user:secret@proxy.example:3128",
		"DALEC_RPM_PROXY_CA_BUNDLE=" + caBundle,
	}

	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, string(out))
	assert.Assert(t, !strings.Contains(string(out), "secret"), string(out))
}

func TestCleanupDnfProxyRestoresTrustBundle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}

	dir := t.TempDir()
	caBundle := filepath.Join(dir, "ca-bundle.pem")
	trustBundle := filepath.Join(dir, "ca-bundle.trust.crt")
	err := os.WriteFile(caBundle, []byte(`system ca
# buildkit proxy CA begin
proxy ca
# buildkit proxy CA end
`), 0o600)
	assert.NilError(t, err)
	err = os.WriteFile(trustBundle, []byte("trust ca\n"), 0o600)
	assert.NilError(t, err)

	cmd := exec.Command("/bin/bash", "-c", dnfProxyConfigScript+`
install_flags="-y"
configure_dnf_proxy
grep -q 'buildkit proxy CA begin' "${DALEC_RPM_PROXY_TRUST_BUNDLE}"
cleanup_dnf_proxy
if grep -q 'buildkit proxy CA begin' "${DALEC_RPM_PROXY_TRUST_BUNDLE}"; then exit 1; fi
`)
	cmd.Env = []string{
		"PATH=/bin:/usr/bin",
		"HTTP_PROXY=http://proxy.example:3128",
		"DALEC_RPM_PROXY_CA_BUNDLE=" + caBundle,
		"DALEC_RPM_PROXY_TRUST_BUNDLE=" + trustBundle,
		"DALEC_RPM_PROXY_TRUST_BUNDLE_BACKUP=" + filepath.Join(dir, "backup"),
	}

	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, string(out))
}

func TestConfigureDnfProxyDisabled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}

	caBundle := filepath.Join(t.TempDir(), "ca-bundle.pem")
	err := os.WriteFile(caBundle, []byte("test ca"), 0o600)
	assert.NilError(t, err)

	cmd := exec.Command("/bin/bash", "-c", dnfProxyConfigScript+`
install_flags="-y"
configure_dnf_proxy
if [ "${install_flags}" != "-y" ]; then exit 1; fi
if [ -n "${SSL_CERT_FILE:-}" ] || [ -n "${CURL_CA_BUNDLE:-}" ]; then exit 1; fi
`)
	cmd.Env = []string{
		"PATH=/bin:/usr/bin",
		"HTTP_PROXY=http://proxy.example:3128",
		"DALEC_RPM_PROXY_CA_BUNDLE=" + caBundle,
		"DALEC_DISABLE_PROXY_CONFIG=1",
	}

	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, string(out))
}
