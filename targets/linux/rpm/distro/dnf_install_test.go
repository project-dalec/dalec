package distro

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec/internal/test"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestConfigureDnfProxyAddsCACertOptions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}

	caBundle := filepath.Join(t.TempDir(), "ca-bundle.pem")
	err := os.WriteFile(caBundle, []byte("test ca"), 0o600)
	assert.NilError(t, err)

	cmd := exec.Command("bash", "-c", dnfProxyConfigScript+`
install_flags="-y"
configure_dnf_proxy
printf '%s\n%s\n%s\n' "${install_flags}" "${SSL_CERT_FILE:-}" "${CURL_CA_BUNDLE:-}"
`)
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
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

	cmd := exec.Command("bash", "-c", "set -x\n"+dnfProxyConfigScript+`
install_flags="-y"
configure_dnf_proxy
`)
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HTTP_PROXY=http://user:secret@proxy.example:3128",
		"DALEC_RPM_PROXY_CA_BUNDLE=" + caBundle,
	}

	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, string(out))
	assert.Assert(t, !strings.Contains(string(out), "secret"), string(out))
}

func TestCleanupDnfProxyRemovesOnlyProxyCABlock(t *testing.T) {
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

	cmd := exec.Command("bash", "-c", dnfProxyConfigScript+`
install_flags="-y"
configure_dnf_proxy
grep -q 'buildkit proxy CA begin' "${DALEC_RPM_PROXY_TRUST_BUNDLE}"
printf 'new ca\n' >> "${DALEC_RPM_PROXY_TRUST_BUNDLE}"
cleanup_dnf_proxy
if grep -q 'buildkit proxy CA begin' "${DALEC_RPM_PROXY_TRUST_BUNDLE}"; then exit 1; fi
grep -q 'new ca' "${DALEC_RPM_PROXY_TRUST_BUNDLE}"
`)
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HTTP_PROXY=http://proxy.example:3128",
		"DALEC_RPM_PROXY_CA_BUNDLE=" + caBundle,
		"DALEC_RPM_PROXY_TRUST_BUNDLE=" + trustBundle,
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

	cmd := exec.Command("bash", "-c", dnfProxyConfigScript+`
install_flags="-y"
configure_dnf_proxy
if [ "${install_flags}" != "-y" ]; then exit 1; fi
if [ -n "${SSL_CERT_FILE:-}" ] || [ -n "${CURL_CA_BUNDLE:-}" ]; then exit 1; fi
`)
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HTTP_PROXY=http://proxy.example:3128",
		"DALEC_RPM_PROXY_CA_BUNDLE=" + caBundle,
		"DALEC_DISABLE_PROXY_CONFIG=1",
	}

	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, string(out))
}

func TestDnfInstallFlagsExclude(t *testing.T) {
	var cfg dnfInstallConfig
	dnfInstallOptions(&cfg, []DnfInstallOpt{DnfAtRoot("/tmp/rootfs"), DnfExclude("foo", "foo-debug")})
	flags := dnfInstallFlags(&cfg)
	assert.Check(t, strings.Contains(flags, "--exclude=foo,foo-debug"), flags)

	cfg = dnfInstallConfig{}
	assert.Check(t, !strings.Contains(dnfInstallFlags(&cfg), "--exclude"))
}

func TestUpgradePassesPackagesAsArguments(t *testing.T) {
	ctx := context.Background()
	for name, upgrade := range map[string]PackageInstaller{"dnf": DnfUpgrade, "tdnf": TdnfUpgrade} {
		t.Run(name, func(t *testing.T) {
			var cfg dnfInstallConfig
			st := llb.Image("worker").Run(upgrade(&cfg, "3.0", []string{"$(touch /pwned)", "openssl"})).Root()

			var args []string
			var scripts string
			for _, op := range test.LLBOpsFromState(ctx, t, st) {
				if e := op.Op.GetExec(); e != nil {
					args = e.Meta.Args
				}
				if f := op.Op.GetFile(); f != nil {
					for _, a := range f.Actions {
						if mk := a.GetMkfile(); mk != nil {
							scripts += string(mk.Data)
						}
					}
				}
			}
			assert.Assert(t, len(args) >= 2)
			assert.Check(t, cmp.DeepEqual(args[len(args)-2:], []string{"$(touch /pwned)", "openssl"}))
			assert.Check(t, !strings.Contains(scripts, "pwned"), "package names must not be interpolated into the script")
			assert.Check(t, strings.Contains(scripts, `dnf_sub_cmd="upgrade"`))
		})
	}
}
