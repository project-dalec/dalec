package distro

import (
	"context"
	"path/filepath"
	"slices"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/pkg/errors"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
	"github.com/project-dalec/dalec/internal/layersplit"
	"github.com/project-dalec/dalec/targets"
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

	rootfs := prev
	// Skip the upgrade when packages were requested but they are all spec
	// packages: there is nothing to upgrade from the repositories.
	if len(pkgs) == 0 || len(upgrade) > 0 {
		upgradeOpts := append(slices.Clone(installOpts), DnfExclude(keep...))
		rootfs = worker.Run(
			dalec.WithConstraints(opts...),
			cfg.Upgrade(upgrade, upgradeOpts...),
			frontend.IgnoreCache(client, targets.IgnoreCacheKeyContainer),
			llb.WithCustomName("Upgrade packages in the existing image"),
		).AddMount(workPath, prev)
	}

	if specPkgs != nil {
		rootfs = worker.Run(
			dalec.WithConstraints(opts...),
			cfg.Install([]string{filepath.Join(rpmMountDir, "*/*.rpm")}, installOpts...),
			llb.AddMount(rpmMountDir, *specPkgs, llb.SourcePath("/RPMS")),
			frontend.IgnoreCache(client, targets.IgnoreCacheKeyContainer),
			llb.WithCustomName("Install spec packages into the existing image"),
		).AddMount(workPath, rootfs)
	}

	return rootfs, nil
}
