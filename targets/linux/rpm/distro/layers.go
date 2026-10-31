package distro

import (
	"github.com/project-dalec/dalec/internal/layersplit"
)

// LayerSplitDumpScript returns a shell script which writes the RPM data
// needed to split a container root filesystem into per-package layers.
//
// The script expects the root filesystem at $1, the spec packages (in the
// /RPMS/<arch>/*.rpm layout) at $2 and writes its output to $3.
func (cfg *Config) LayerSplitDumpScript() string {
	return `set -eu
root="$1"
pkgs="$2"
out="$3"

rpm --root "${root}" -qa --qf '%{NAME}\t%{SOURCERPM}\n' > "${out}/` + layersplit.RPMPackagesFile + `"
rpm --root "${root}" -qa --qf '[%{=NAME}\t%{FILESTATES:fstate}\t%{FILEFLAGS:fflags}\t%{FILENAMES}\n]' > "${out}/` + layersplit.RPMFilesFile + `"

# rpm -V exits non-zero when it finds differences, which is expected.
rpm --root "${root}" -Va --nodeps --noscripts > "${out}/` + layersplit.RPMVerifyFile + `" || true

: > "${out}/` + layersplit.AppPackagesFile + `"
for f in "${pkgs}"/RPMS/*/*.rpm; do
	[ -f "${f}" ] || continue
	rpm -qp --qf '%{NAME}\n' "${f}" >> "${out}/` + layersplit.AppPackagesFile + `"
done
`
}
