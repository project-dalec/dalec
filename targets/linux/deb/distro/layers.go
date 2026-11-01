package distro

import (
	"context"
	"strconv"
	"strings"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/pkg/errors"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
	"github.com/project-dalec/dalec/internal/layersplit"
	"github.com/project-dalec/dalec/targets"
	"github.com/project-dalec/dalec/targets/linux"
)

// LayerSplitDumpScript returns a shell script which writes the dpkg data
// needed to split a container root filesystem into per-package layers.
//
// The script expects the root filesystem at $1, the spec packages (*.deb) at
// $2 and writes its output to $3. Spec package names can also be listed, one
// per line, in $2/app.txt.
func (cfg *Config) LayerSplitDumpScript() string {
	return `set -eu
root="$1"
pkgs="$2"
out="$3"
admin="${root}/var/lib/dpkg"
tab="$(printf '\t')"

# Packages with files on disk and their source package.
dpkg-query --admindir="${admin}" -W -f='${db:Status-Status}\t${Package}\t${Source}\n' |
	awk -F"${tab}" -v OFS="${tab}" '$1 != "not-installed" && $1 != "config-files" { print $2, $3 }' > "${out}/` + layersplit.SourcesFile + `"

# Files of those packages, in the RPM file list format.
: > "${out}/` + layersplit.RPMFilesFile + `"
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
	' "${out}/` + layersplit.SourcesFile + `" "${admin}"/info/*.list >> "${out}/` + layersplit.RPMFilesFile + `"
fi

# Files changed after install. dpkg --verify exits non-zero when it finds
# differences, which is expected.
dpkg --root="${root}" --verify > "${out}/` + layersplit.RPMVerifyFile + `" 2>/dev/null || true

# Diverted paths hold a different package's file than the one listed, so
# treat both sides of each diversion as changed.
if [ -f "${admin}/diversions" ]; then
	awk 'NR % 3 != 0 { print "diverted " $0 }' "${admin}/diversions" >> "${out}/` + layersplit.RPMVerifyFile + `"
fi

: > "${out}/` + layersplit.AppPackagesFile + `"
if [ -f "${pkgs}/` + layersplit.AppPackagesFile + `" ]; then
	cat "${pkgs}/` + layersplit.AppPackagesFile + `" >> "${out}/` + layersplit.AppPackagesFile + `"
fi
for f in "${pkgs}"/*.deb; do
	[ -f "${f}" ] || continue
	dpkg-deb -f "${f}" Package >> "${out}/` + layersplit.AppPackagesFile + `"
done
`
}

const (
	updateDir        = "/tmp/dalec/internal/update"
	updateDebsPath   = updateDir + "/debs"
	updateListsPath  = updateDir + "/lists"
	updateRootPath   = updateDir + "/rootfs"
	updateLowerPath  = updateDir + "/lower"
	updateToolsPath  = updateDir + "/tools"
	updateScriptPath = updateDir + "/scripts"
)

// updateDownloadScript downloads the packages needed to update the root
// filesystem at updateRootPath into updateDebsPath, using the worker's apt
// with the dpkg database of the root filesystem.
const updateDownloadScript = `#!/bin/sh
set -eux

` + aptProxyConfigScript + `

configure_apt_proxy
trap cleanup_apt_proxy EXIT

rootfs=` + updateRootPath + `
out=` + updateDebsPath + `
lists=` + updateListsPath + `
admin="${rootfs}/var/lib/dpkg"

# Same as a fresh build: drop cached data from local repos and refresh the
# package index.
rm -f /var/lib/apt/lists/_*
rm -rf /var/lib/apt/lists/partial/*
apt update

# Spec packages are replaced by the supplied spec packages, if any, and are
# otherwise kept as they are: hold them so apt never changes them.
local_debs=""
supplied=""
for f in /spec-packages/*.deb; do
    [ -f "${f}" ] || continue
    local_debs="${local_debs} ${f}"
    supplied="${supplied} $(dpkg-deb -f "${f}" Package)"
done
hold=""
for k in $(cat "${lists}/keep"); do
    case " ${supplied} " in *" ${k} "*) continue ;; esac
    hold="${hold} ${k}"
done

status=/tmp/dalec-update-status
awk -v hold="${hold}" '
    BEGIN { n = split(hold, h, " "); for (i = 1; i <= n; i++) held[h[i]] = 1 }
    /^Package: / { pkg = $2 }
    /^Status: install / && (pkg in held) { sub(/^Status: install /, "Status: hold ") }
    { print }
' "${admin}/status" > "${status}"

mkdir -p "${out}/partial"
apt_opts="-o Dir::State::status=${status} -o Dir::Cache::Archives=${out} --yes --no-remove"

# Package names may contain glob characters, which must not expand to files.
set -f
pkgs=$(cat "${lists}/packages")
if [ -z "${pkgs}" ]; then
    # Every installed package with an available update.
    pkgs=$(apt-get ${apt_opts} --simulate --with-new-pkgs upgrade | awk '/^Inst / { print $2 }')
fi

if [ -n "${pkgs}" ] || [ -n "${local_debs}" ]; then
    # --only-upgrade skips named packages that are not installed, but still
    # installs new dependencies of the upgraded packages.
    apt-get ${apt_opts} --download-only install --only-upgrade ${pkgs} ${local_debs}
fi
rm -rf "${out}/partial" "${out}/lock"

for f in ${local_debs}; do
    cp "${f}" "${out}/"
done

ls -l "${out}"
`

// updateInstallScript installs the downloaded packages. It runs on the root
// filesystem being updated merged on top of the worker, so maintainer scripts
// can use the worker's tools even when the image has no shell.
const updateInstallScript = `#!/bin/sh
set -eux

debs=` + updateDebsPath + `
if ! ls "${debs}"/*.deb > /dev/null 2>&1; then
    echo "No packages to update"
    exit 0
fi

# The worker may exclude paths (such as docs) from installed packages, which
# fresh builds of minimal images do not do.
if [ "${DALEC_MINIMAL_IMAGE}" = "true" ]; then
    rm -f /etc/dpkg/dpkg.cfg.d/excludes
fi

mkdir -p /var/lib/dpkg/info /var/lib/dpkg/updates /var/lib/dpkg/triggers /var/lib/dpkg/alternatives
dpkg --force-confdef --force-confold --install "${debs}"/*.deb
dpkg --force-confdef --force-confold --configure --pending
`

// updateCleanupScript removes what the install left behind: the worker's own
// files, directories that fresh builds of minimal images remove, logs and
// caches.
var updateCleanupScript = `#!/bin/sh
set -eux

root=` + updateRootPath + `
debs=` + updateDebsPath + `

# Mount points created for the install.
rm -rf "${root}` + updateDir + `"
rmdir "${root}/tmp/dalec/internal" "${root}/tmp/dalec" 2>/dev/null || true

# Files of the installed packages are kept even when the worker has them too.
keep=/tmp/dalec-update-keep
: > "${keep}"
for f in "${debs}"/*.deb; do
    [ -f "${f}" ] || continue
    pkg=$(dpkg-deb -f "${f}" Package)
    for list in "${root}/var/lib/dpkg/info/${pkg}.list" "${root}/var/lib/dpkg/info/${pkg}":*.list; do
        [ -f "${list}" ] && cat "${list}" >> "${keep}"
    done
done

` + linux.PruneToolsScript(updateRootPath, updateLowerPath, updateToolsPath, "${keep}") + `

# Everything below only applies to minimal images, which are cleaned up
# the same way when they are built.
if [ "${DALEC_MINIMAL_IMAGE}" != "true" ]; then
    exit 0
fi

# A library trigger run during the install saw the worker's libraries too.
if ls "${debs}"/*.deb > /dev/null 2>&1 && [ -f "${root}/etc/ld.so.conf" ] && [ -f "${root}/etc/ld.so.cache" ]; then
    ldconfig -r "${root}"
fi

cleanup_dirs="` + debCleanupDirs + `"
keep_systemd=0
if [ -x "${root}/usr/lib/systemd/systemd" ] || [ -x "${root}/lib/systemd/systemd" ]; then
    keep_systemd=1
fi
case "$(dpkg-query --admindir="${root}/var/lib/dpkg" -W -f='${db:Status-Status}\n' systemd 2>/dev/null)" in
    installed|half-configured|triggers-awaited|triggers-pending) keep_systemd=1 ;;
esac
if [ "${keep_systemd}" != "1" ]; then
    cleanup_dirs="${cleanup_dirs}
/etc/systemd
/var/lib/systemd
"
fi
if [ "${DALEC_KEEP_USR_SHARE_DOC}" != "true" ]; then
    cleanup_dirs="${cleanup_dirs}
/usr/share/doc
/usr/share/man
/usr/share/info
"
fi
for d in ${cleanup_dirs}; do
    rm -rf "${root}${d}"
done

# Maintainer scripts are not kept in minimal images.
rm -f "${root}"/var/lib/dpkg/info/*.prerm \
      "${root}"/var/lib/dpkg/info/*.postrm \
      "${root}"/var/lib/dpkg/info/*.preinst \
      "${root}"/var/lib/dpkg/info/*.postinst

if [ -d "${root}/var/log" ]; then
    find "${root}/var/log" -mindepth 1 -delete
fi
`

// UpdateContainer upgrades the packages installed in prev, an existing
// container root filesystem, instead of building it again. Only the updated
// packages (and any new dependencies they need) are downloaded.
//
// pkgs limits the upgrade to the named packages; when empty, every package
// with an available update is upgraded. The packages in keep (the spec's own
// packages) are never upgraded from the package repositories. When specPkgs
// is set (a state with *.deb files), those spec packages are installed or
// upgraded from it; otherwise the spec packages in prev are kept as they are.
//
// Minimal images have no shell or package manager, so the packages are
// installed on prev merged on top of the worker, whose tools run the
// maintainer scripts. The worker's files are removed afterwards.
func (c *Config) UpdateContainer(ctx context.Context, client gwclient.Client, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, prev llb.State, specPkgs *llb.State, pkgs, keep []string, opts ...llb.ConstraintsOpt) (llb.State, error) {
	if !frontend.SupportsDiffMerge(client) {
		return llb.State{}, errors.New("updating an existing image requires a BuildKit version with diff/merge support")
	}

	opts = append(opts, dalec.ProgressGroup("Update packages"), frontend.IgnoreCache(client))

	worker := c.Worker(sOpt, dalec.Platform(sOpt.TargetPlatform), dalec.WithConstraints(opts...))

	// Spec packages are only changed through specPkgs, never from the
	// package repositories, even when the same package is published there.
	kept := make(map[string]bool, len(keep))
	for _, k := range keep {
		kept[k] = true
	}
	var upgrade []string
	for _, p := range pkgs {
		if !kept[p] {
			upgrade = append(upgrade, p)
		}
	}
	if len(pkgs) > 0 && len(upgrade) == 0 && specPkgs == nil {
		// Only spec packages were requested and none were supplied: there
		// is nothing to update. Reapply the symlinks for spec changes.
		return linux.ReapplyPostSymlinks(client, spec, targetKey, worker, prev, opts...), nil
	}

	list := func(names []string) []byte {
		if len(names) == 0 {
			return nil
		}
		return []byte(strings.Join(names, "\n") + "\n")
	}
	lists := llb.Scratch().
		File(llb.Mkfile("keep", 0o644, list(keep)), dalec.WithConstraints(opts...)).
		File(llb.Mkfile("packages", 0o644, list(upgrade)), dalec.WithConstraints(opts...))
	scripts := llb.Scratch().
		File(llb.Mkfile("download.sh", 0o755, []byte(updateDownloadScript)), dalec.WithConstraints(opts...)).
		File(llb.Mkfile("install.sh", 0o755, []byte(updateInstallScript)), dalec.WithConstraints(opts...)).
		File(llb.Mkfile("cleanup.sh", 0o755, []byte(updateCleanupScript)), dalec.WithConstraints(opts...))

	specSt := llb.Scratch()
	if specPkgs != nil {
		specSt = *specPkgs
	}

	input := buildContainerInput{Config: c, Client: client, Worker: worker, SOpt: sOpt, Spec: spec, Target: targetKey, Opts: opts}

	// The previous image must have a dpkg database to update.
	prev = worker.Run(
		dalec.WithConstraints(opts...),
		dalec.ShArgs(`[ -s `+updateRootPath+`/var/lib/dpkg/status ] || { echo "the `+dalec.PreviousImageContext+` image has no dpkg package database" >&2; exit 1; }`),
		llb.WithCustomName("Check the existing image has a dpkg package database"),
	).AddMount(updateRootPath, prev)

	debs := worker.Run(
		dalec.WithConstraints(opts...),
		llb.AddMount(updateScriptPath, scripts, llb.Readonly),
		llb.AddMount(updateListsPath, lists, llb.Readonly),
		llb.AddMount(updateRootPath, prev, llb.Readonly),
		llb.AddMount("/spec-packages", specSt, llb.Readonly),
		extraRepos(input, opts...),
		dalec.WithMountedAptCache(c.AptCachePrefix, opts...),
		llb.AddEnv("DEBIAN_FRONTEND", "noninteractive"),
		aptProxyConfig(sOpt),
		llb.Args([]string{"/bin/sh", updateScriptPath + "/download.sh"}),
		frontend.IgnoreCache(client, targets.IgnoreCacheKeyContainer),
		llb.WithCustomName("Download updated packages"),
	).AddMount(updateDebsPath, llb.Scratch())

	// prev is on top, so its files (including the dpkg database) win over
	// the worker's.
	installed := llb.Merge([]llb.State{worker, prev}, dalec.WithConstraints(opts...)).Run(
		dalec.WithConstraints(opts...),
		llb.AddMount(updateScriptPath, scripts, llb.Readonly),
		llb.AddMount(updateDebsPath, debs, llb.Readonly),
		llb.AddEnv("DEBIAN_FRONTEND", "noninteractive"),
		llb.AddEnv("DALEC_MINIMAL_IMAGE", strconv.FormatBool(c.DefaultOutputImage == "")),
		llb.Args([]string{"/bin/sh", updateScriptPath + "/install.sh"}),
		frontend.IgnoreCache(client, targets.IgnoreCacheKeyContainer),
		llb.WithCustomName("Install updated packages"),
	).Root()

	minimal := llb.AddEnv("DALEC_MINIMAL_IMAGE", strconv.FormatBool(c.DefaultOutputImage == ""))

	artifacts := spec.GetArtifacts(targetKey)
	keepUsrShareDoc := artifacts.HasDocs() || len(artifacts.Licenses) > 0

	rootfs := worker.Run(
		dalec.WithConstraints(opts...),
		llb.AddMount(updateScriptPath, scripts, llb.Readonly),
		llb.AddMount(updateDebsPath, debs, llb.Readonly),
		llb.AddMount(updateLowerPath, prev, llb.Readonly),
		llb.AddMount(updateToolsPath, worker, llb.Readonly),
		linux.WithLayerSplitFrontend(client),
		llb.AddEnv("DALEC_KEEP_USR_SHARE_DOC", strconv.FormatBool(keepUsrShareDoc)),
		minimal,
		llb.Args([]string{"/bin/sh", updateScriptPath + "/cleanup.sh"}),
		llb.WithCustomName("Remove build tools from the updated image"),
	).AddMount(updateRootPath, installed)

	return linux.ReapplyPostSymlinks(client, spec, targetKey, worker, rootfs, opts...), nil
}
