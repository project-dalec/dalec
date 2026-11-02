package distro

import (
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/internal/layersplit"
)

// LayerSplitDumpScript returns a shell script which writes the dpkg data
// needed to split a container root filesystem into one layer per package set.
//
// The script expects the root filesystem at $1, the spec packages (*.deb) at
// $2 and writes its output to $3.
func (c *Config) LayerSplitDumpScript() string {
	return `set -eu
root="$1"
pkgs="$2"
out="$3"
admin="${root}/var/lib/dpkg"
tab="$(printf '\t')"

# Packages with files on disk and their dependencies. Recommends count too,
# since only installed packages are taken into account.
dpkg-query --admindir="${admin}" -W -f='${db:Status-Status}\t${Package}\t${Pre-Depends}\t${Depends}\t${Recommends}\t${Provides}\n' |
	awk -F"${tab}" -v OFS="${tab}" '$1 != "not-installed" && $1 != "config-files" { print $2, $3, $4, $5, $6 }' > "${out}/` + layersplit.DpkgDepsFile + `"
cut -f1 "${out}/` + layersplit.DpkgDepsFile + `" > "${out}/` + layersplit.PackagesFile + `"

# Files of those packages, in the RPM file list format.
: > "${out}/` + layersplit.FilesFile + `"
if ls "${admin}"/info/*.list > /dev/null 2>&1; then
	awk -F"${tab}" -v OFS="${tab}" '
		NR == FNR { pkgs[$1] = 1; next }
		FNR == 1 {
			name = FILENAME
			sub(/.*\//, "", name)
			sub(/\.list$/, "", name)
			sub(/:.*/, "", name)
		}
		(name in pkgs) && /^\// { print name, "normal", "", $0 }
	' "${out}/` + layersplit.PackagesFile + `" "${admin}"/info/*.list >> "${out}/` + layersplit.FilesFile + `"
fi

# Files changed after install. dpkg --verify exits non-zero when it finds
# differences, which is expected.
dpkg --root="${root}" --verify > "${out}/` + layersplit.VerifyFile + `" 2>/dev/null || true

# Diverted paths hold a different package's file than the one listed, so
# treat both sides of each diversion as changed.
if [ -f "${admin}/diversions" ]; then
	awk 'NR % 3 != 0 { print "diverted " $0 }' "${admin}/diversions" >> "${out}/` + layersplit.VerifyFile + `"
fi

# dpkg --verify only checks file contents. Ownership and modes set with
# dpkg-statoverride (for example by a maintainer script) differ from the
# package, so treat those paths as changed too.
if [ -f "${admin}/statoverride" ]; then
	awk 'NF >= 4 { print "statoverride " $4 }' "${admin}/statoverride" >> "${out}/` + layersplit.VerifyFile + `"
fi

: > "${out}/` + layersplit.AppPackagesFile + `"
for f in "${pkgs}"/*.deb; do
	[ -f "${f}" ] || continue
	dpkg-deb -f "${f}" Package >> "${out}/` + layersplit.AppPackagesFile + `"
done
`
}

// LayerSplitBasePackages returns the base packages of minimal containers,
// which are built from scratch and always keep base-files. Containers built
// on top of the distro's default image are not split, so that image's layers
// are kept as they are.
func (c *Config) LayerSplitBasePackages(*dalec.Spec, string) ([]string, bool) {
	if c.DefaultOutputImage != "" {
		return nil, false
	}
	return []string{"base-files"}, true
}
