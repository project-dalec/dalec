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

func TestConfigureAptProxyWritesConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}

	conf := filepath.Join(t.TempDir(), "apt.conf")
	cmd := exec.Command("/bin/sh", "-c", aptProxyConfigScript+"\nconfigure_apt_proxy\nprintf '%s' \"${APT_CONFIG}\"\n")
	cmd.Env = []string{
		"PATH=/bin:/usr/bin",
		"HTTP_PROXY=http://proxy.example:3128",
		"HTTPS_PROXY=https://proxy.example:8443",
		"DALEC_APT_PROXY_CONFIG=" + conf,
	}

	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, string(out))
	assert.Equal(t, string(out), conf)

	dt, err := os.ReadFile(conf)
	assert.NilError(t, err)

	aptConf := string(dt)
	assert.Assert(t, strings.Contains(aptConf, `Acquire::http::Proxy "http://proxy.example:3128";`), aptConf)
	assert.Assert(t, strings.Contains(aptConf, `Acquire::https::Proxy "https://proxy.example:8443";`), aptConf)

	info, err := os.Stat(conf)
	assert.NilError(t, err)
	assert.Equal(t, info.Mode().Perm(), os.FileMode(0o600))
}

func TestConfigureAptProxyDoesNotTraceProxyValues(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}

	conf := filepath.Join(t.TempDir(), "apt.conf")
	cmd := exec.Command("/bin/sh", "-c", "set -x\n"+aptProxyConfigScript+"\nconfigure_apt_proxy\n")
	cmd.Env = []string{
		"PATH=/bin:/usr/bin",
		"HTTP_PROXY=http://user:secret@proxy.example:3128",
		"DALEC_APT_PROXY_CONFIG=" + conf,
	}

	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, string(out))
	assert.Assert(t, !strings.Contains(string(out), "secret"), string(out))
}

func TestCleanupAptProxyRemovesConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}

	conf := filepath.Join(t.TempDir(), "apt.conf")
	cmd := exec.Command("/bin/sh", "-c", aptProxyConfigScript+"\nconfigure_apt_proxy\ncleanup_apt_proxy\nif [ -n \"${APT_CONFIG:-}\" ] || [ -e \"${DALEC_APT_PROXY_CONFIG}\" ]; then exit 1; fi\n")
	cmd.Env = []string{
		"PATH=/bin:/usr/bin",
		"HTTP_PROXY=http://proxy.example:3128",
		"DALEC_APT_PROXY_CONFIG=" + conf,
	}

	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, string(out))
}

func TestCleanupAptProxyRestoresAptConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}

	dir := t.TempDir()
	conf := filepath.Join(dir, "apt.conf")
	original := filepath.Join(dir, "original.conf")
	cmd := exec.Command("/bin/sh", "-c", aptProxyConfigScript+"\nconfigure_apt_proxy\ncleanup_apt_proxy\nprintf '%s' \"${APT_CONFIG:-}\"\n")
	cmd.Env = []string{
		"PATH=/bin:/usr/bin",
		"APT_CONFIG=" + original,
		"HTTP_PROXY=http://proxy.example:3128",
		"DALEC_APT_PROXY_CONFIG=" + conf,
	}

	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, string(out))
	assert.Equal(t, string(out), original)
}

func TestConfigureAptProxyDisabled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}

	conf := filepath.Join(t.TempDir(), "apt.conf")
	cmd := exec.Command("/bin/sh", "-c", aptProxyConfigScript+"\nconfigure_apt_proxy\nif [ -n \"${APT_CONFIG:-}\" ] || [ -e \"${DALEC_APT_PROXY_CONFIG}\" ]; then exit 1; fi\n")
	cmd.Env = []string{
		"PATH=/bin:/usr/bin",
		"HTTP_PROXY=http://proxy.example:3128",
		"DALEC_APT_PROXY_CONFIG=" + conf,
		"DALEC_DISABLE_PROXY_CONFIG=1",
	}

	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, string(out))
}
