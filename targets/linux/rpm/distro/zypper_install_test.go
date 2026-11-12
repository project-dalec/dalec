package distro

import (
	"errors"
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

	cmd := exec.Command("bash", "-c", zypperProxyConfigScript+`
configure_zypper_proxy
grep -q 'buildkit proxy CA begin' "${DALEC_ZYPPER_PROXY_ANCHOR}"
cleanup_zypper_proxy
if [ -e "${DALEC_ZYPPER_PROXY_ANCHOR}" ]; then exit 1; fi
if [ "$(wc -l < "${DALEC_ZYPPER_UPDATE_LOG}")" -ne 2 ]; then exit 1; fi
`)
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
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
	cmd := exec.Command("bash", "-c", "set -x\n"+zypperProxyConfigScript+`
configure_zypper_proxy
`)
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
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

	cmd := exec.Command("bash", "-c", zypperProxyConfigScript+`
configure_zypper_proxy
if [ -e "${DALEC_ZYPPER_PROXY_ANCHOR}" ]; then exit 1; fi
`)
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HTTP_PROXY=http://proxy.example:3128",
		"DALEC_RPM_PROXY_CA_BUNDLE=" + caBundle,
		"DALEC_ZYPPER_PROXY_ANCHOR=" + filepath.Join(t.TempDir(), "proxy.pem"),
		"DALEC_DISABLE_PROXY_CONFIG=1",
	}

	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, string(out))
}

// TestZypperUpgradeScript runs the upgrade script with stub zypper and rpm
// commands.
func TestZypperUpgradeScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}

	script, err := zypperScript(&dnfInstallConfig{root: "/tmp/rootfs"}, []string{"update"})
	assert.NilError(t, err)

	run := func(t *testing.T, listUpdates string, args ...string) (string, error) {
		dir := t.TempDir()
		bin := filepath.Join(dir, "bin")
		assert.NilError(t, os.MkdirAll(bin, 0o755))
		log := filepath.Join(dir, "zypper.log")
		zypper := `#!/bin/sh
for a in "$@"; do
	case "$a" in
	list-updates) ` + listUpdates + ` ;;
	update) echo "update: $*" >> "` + log + `" ;;
	esac
done
`
		assert.NilError(t, os.WriteFile(filepath.Join(bin, "zypper"), []byte(zypper), 0o755))
		assert.NilError(t, os.WriteFile(filepath.Join(bin, "rpm"), []byte("#!/bin/sh\necho spec-1.0\n"), 0o755))
		scriptPath := filepath.Join(dir, "install.sh")
		assert.NilError(t, os.WriteFile(scriptPath, script, 0o755))

		cmd := exec.Command("bash", append([]string{scriptPath}, args...)...)
		cmd.Dir = dir
		cmd.Env = []string{
			"PATH=" + bin + ":" + os.Getenv("PATH"),
			"DALEC_DISABLE_PROXY_CONFIG=1",
			"DALEC_ZYPP_CONF=" + filepath.Join(dir, "zypp.conf"),
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			err = errors.New(string(out))
		}
		dt, _ := os.ReadFile(log)
		return string(dt), err
	}

	t.Run("excludes are left out of the listed updates", func(t *testing.T) {
		log, err := run(t, `echo '<update-list><update kind="package" name="openssl" edition="2"/><update kind="package" name="spec" edition="2"/></update-list>'`, "--exclude=spec")
		assert.NilError(t, err)
		assert.Assert(t, strings.Contains(log, "openssl"), log)
		assert.Assert(t, !strings.Contains(log, "spec"), log)
	})

	t.Run("wildcard selectors are passed as is", func(t *testing.T) {
		log, err := run(t, "exit 1", "--exclude=spec", "glibc*")
		assert.NilError(t, err)
		assert.Assert(t, strings.Contains(log, "glibc*"), log)
	})

	t.Run("failing to list updates fails", func(t *testing.T) {
		log, err := run(t, "exit 1", "--exclude=spec")
		assert.Assert(t, err != nil)
		assert.Equal(t, log, "")
	})
}
