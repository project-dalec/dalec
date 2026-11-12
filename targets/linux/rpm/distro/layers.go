package distro

import (
	"context"
	"path/filepath"
	"slices"
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

// LayerSplitDumpScript returns a shell script which writes the RPM data
// needed to split a container root filesystem into per-package layers.
//
// The script expects the root filesystem at $1, the spec packages (in the
// /RPMS/<arch>/*.rpm layout) at $2 and writes its output to $3. Spec package
// names can also be listed, one per line, in $2/app.txt.
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
if [ -f "${pkgs}/` + layersplit.AppPackagesFile + `" ]; then
	cat "${pkgs}/` + layersplit.AppPackagesFile + `" >> "${out}/` + layersplit.AppPackagesFile + `"
fi
for f in "${pkgs}"/RPMS/*/*.rpm; do
	[ -f "${f}" ] || continue
	rpm -qp --qf '%{NAME}\n' "${f}" >> "${out}/` + layersplit.AppPackagesFile + `"
done
`
}

// UpdateContainer upgrades the packages installed in prev, an existing
// container root filesystem, instead of installing every package again. Only
// the updated packages (and any new dependencies they need) are downloaded.
//
// pkgs limits the upgrade to the named packages; when empty, every package
// with an available update is upgraded. The packages in keep (the spec's own
// packages) are never upgraded from the package repositories. When specPkgs
// is set (a state with the /RPMS/<arch>/*.rpm layout), those spec packages are
// installed or upgraded from it; otherwise the spec packages in prev are kept
// as they are.
func (cfg *Config) UpdateContainer(ctx context.Context, client gwclient.Client, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, prev llb.State, specPkgs *llb.State, pkgs, keep []string, opts ...llb.ConstraintsOpt) (llb.State, error) {
	if cfg.UpgradeFunc == nil {
		return llb.State{}, errors.Errorf("updating an existing image is not supported for target %s", targetKey)
	}

	opts = append(opts, dalec.ProgressGroup("Update packages"), frontend.IgnoreCache(client))

	const (
		workPath    = "/tmp/rootfs"
		rpmMountDir = "/tmp/rpms"
	)

	repoMounts, keyPaths := cfg.RepoMounts(spec.GetInstallRepos(targetKey), sOpt, opts...)
	installOpts := []DnfInstallOpt{
		DnfAtRoot(workPath),
		DnfWithMounts(repoMounts),
		DnfImportKeys(keyPaths),
		DnfWithSourceOpts(sOpt),
		DnfInstallWithConstraints(opts),
		IncludeDocs(spec.GetArtifacts(targetKey).HasDocs()),
	}

	worker := cfg.Worker(sOpt, dalec.Platform(sOpt.TargetPlatform), dalec.WithConstraints(opts...))

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

	// The previous image must have an RPM database to upgrade.
	rootfs := worker.Run(
		dalec.WithConstraints(opts...),
		dalec.ShArgs(`[ -n "$(rpm --root `+workPath+` -qa 2>/dev/null)" ] || { echo "the `+dalec.PreviousImageContext+` image has no RPM package database" >&2; exit 1; }`),
		llb.WithCustomName("Check the existing image has an RPM package database"),
	).AddMount(workPath, prev)

	runUpgrade := len(pkgs) == 0 || len(upgrade) > 0
	runInstall := specPkgs != nil

	// Re-run minimization after the last package transaction, so packages
	// pulled in by the update are minimized like in a fresh build. The spec
	// and base packages are passed as seeds since the spec packages may not
	// be mounted.
	var minimizeOpts []DnfInstallOpt
	if spec.GetImageMinimizationProfile(targetKey) == dalec.ImageMinimizationProfileDefault {
		seedSt := seedsState(keep, cfg.BasePackages, opts...)
		minimizeOpts = []DnfInstallOpt{
			DnfWithMounts(llb.AddMount(minimizeSeedsPath, seedSt, llb.SourcePath("seeds"), llb.Readonly)),
			minimizeInstall(opts...),
		}
	}

	// Skip the upgrade when packages were requested but they are all spec
	// packages: there is nothing to upgrade from the repositories.
	if runUpgrade {
		upgradeOpts := append(slices.Clone(installOpts), DnfExclude(keep...))
		if !runInstall {
			upgradeOpts = append(upgradeOpts, minimizeOpts...)
		}
		rootfs = worker.Run(
			dalec.WithConstraints(opts...),
			cfg.Upgrade(upgrade, upgradeOpts...),
			frontend.IgnoreCache(client, targets.IgnoreCacheKeyContainer),
			llb.WithCustomName("Upgrade packages in the existing image"),
		).AddMount(workPath, rootfs)
	}

	// Without any package transaction, minimization still has to run so the
	// requested profile applies (for example to a previously unminimized image).
	if !runUpgrade && !runInstall && minimizeOpts != nil {
		rootfs = worker.Run(
			dalec.WithConstraints(opts...),
			minimizeRun(seedsState(keep, cfg.BasePackages, opts...), opts...),
			llb.WithCustomName("Minimize the existing image"),
		).AddMount(workPath, rootfs)
	}

	if runInstall {
		rootfs = worker.Run(
			dalec.WithConstraints(opts...),
			cfg.Install([]string{filepath.Join(rpmMountDir, "*/*.rpm")}, append(slices.Clone(installOpts), minimizeOpts...)...),
			llb.AddMount(rpmMountDir, *specPkgs, llb.SourcePath("/RPMS")),
			frontend.IgnoreCache(client, targets.IgnoreCacheKeyContainer),
			llb.WithCustomName("Install spec packages into the existing image"),
		).AddMount(workPath, rootfs)
	}

	// Re-apply the configured symlinks: the spec may add new ones and a
	// package update may have replaced existing ones.
	return linux.ReapplyPostSymlinks(client, spec, targetKey, worker, rootfs, opts...), nil
}

// seedsState returns a state with the minimization seed list: the kept spec
// packages and the distro's base packages.
func seedsState(keep []string, base []dalec.Spec, opts ...llb.ConstraintsOpt) llb.State {
	seeds := slices.Clone(keep)
	for _, b := range base {
		seeds = append(seeds, b.Name)
	}
	return llb.Scratch().File(llb.Mkfile("seeds", 0o644, []byte(strings.Join(seeds, "\n")+"\n")), dalec.WithConstraints(opts...))
}
