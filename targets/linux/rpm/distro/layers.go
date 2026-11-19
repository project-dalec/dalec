package distro

import (
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/internal/layersplit"
)

// LayerSplitDumpScript returns a shell script which writes the RPM data
// needed to split a container root filesystem into one layer per package set.
//
// The script expects the root filesystem at $1, the spec packages (in the
// /RPMS/<arch>/*.rpm layout) at $2 and writes its output to $3.
func (cfg *Config) LayerSplitDumpScript() string {
	return `set -eu
root="$1"
pkgs="$2"
out="$3"

rpm --root "${root}" -qa --qf '%{NAME}\n' > "${out}/` + layersplit.PackagesFile + `"
rpm --root "${root}" -qa --qf '[%{=NAME}\t%{FILESTATES:fstate}\t%{FILEFLAGS:fflags}\t%{FILENAMES}\n]' > "${out}/` + layersplit.FilesFile + `"
# Weak dependencies (recommends) count too: only installed packages are taken
# into account.
rpm --root "${root}" -qa --qf '[%{=NAME}\t%{REQUIRENAME}\n][%{=NAME}\t%{RECOMMENDNAME}\n]' > "${out}/` + layersplit.RPMRequiresFile + `"
rpm --root "${root}" -qa --qf '[%{=NAME}\t%{PROVIDENAME}\n]' > "${out}/` + layersplit.RPMProvidesFile + `"

# rpm -V exits non-zero when it finds differences, which is expected.
rpm --root "${root}" -Va --nodeps --noscripts > "${out}/` + layersplit.VerifyFile + `" || true

: > "${out}/` + layersplit.AppPackagesFile + `"
for f in "${pkgs}"/RPMS/*/*.rpm; do
	[ -f "${f}" ] || continue
	rpm -qp --qf '%{NAME}\n' "${f}" >> "${out}/` + layersplit.AppPackagesFile + `"
done
`
}

// LayerSplitBasePackages returns the names of the distro's base packages,
// which are installed in every container built from scratch.
func (cfg *Config) LayerSplitBasePackages(*dalec.Spec, string) ([]string, bool) {
	names := make([]string, 0, len(cfg.BasePackages))
	for _, b := range cfg.BasePackages {
		names = append(names, b.Name)
	}
	return names, true
}
