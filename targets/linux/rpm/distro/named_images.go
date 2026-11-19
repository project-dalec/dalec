package distro

import (
	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
)

const rpmImagePackagesScript = `#!/usr/bin/env bash
set -euo pipefail

mkdir -p /packages/selected
createrepo_c /packages/repo
for name in "$@"; do
	found=0
	for file in /packages/repo/*/*.rpm; do
		[ -f "$file" ] || continue
		if [ "$(rpm -qp --qf '%{NAME}' "$file")" = "$name" ]; then
			cp "$file" /packages/selected/
			found=1
		fi
	done
	if [ "$found" = 0 ]; then
		echo "requested package $name was not found in the built RPMs" >&2
		exit 1
	fi
done
`

func (cfg *Config) imagePackageRepo(worker, packages llb.State, selected []string, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) llb.State {
	worker = worker.Run(
		cfg.Install([]string{"createrepo_c"}, DnfWithSourceOpts(sOpt), DnfInstallWithConstraints(opts)),
		dalec.WithConstraints(opts...),
	).Root()
	script := llb.Scratch().File(llb.Mkfile("select.sh", 0o755, []byte(rpmImagePackagesScript)), opts...)
	repo := llb.Scratch().File(llb.Copy(packages, "/RPMS", "/repo", &llb.CopyInfo{CopyDirContentsOnly: true}), opts...)
	return worker.Run(
		llb.AddMount("/tmp/dalec/select-image-packages.sh", script, llb.SourcePath("select.sh"), llb.Readonly),
		llb.Args(append([]string{"/tmp/dalec/select-image-packages.sh"}, selected...)),
		dalec.WithConstraints(opts...),
	).AddMount("/packages", repo)
}
