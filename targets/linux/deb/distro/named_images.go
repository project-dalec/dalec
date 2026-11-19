package distro

import (
	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
)

const debImagePackagesScript = `#!/bin/sh
set -eu

mkdir -p /packages/selected
cd /packages/repo
dpkg-scanpackages --multiversion . /dev/null > Packages
for name in "$@"; do
	found=0
	for file in ./*.deb; do
		[ -f "$file" ] || continue
		if [ "$(dpkg-deb -f "$file" Package)" = "$name" ]; then
			cp "$file" /packages/selected/
			found=1
		fi
	done
	if [ "$found" = 0 ]; then
		echo "requested package $name was not found in the built DEBs" >&2
		exit 1
	fi
done
`

func imagePackageRepo(input buildContainerInput, selected []string) llb.State {
	script := llb.Scratch().File(llb.Mkfile("select.sh", 0o755, []byte(debImagePackagesScript)), input.Opts...)
	repo := llb.Scratch().File(llb.Copy(input.SpecPackages, "/", "/repo", &llb.CopyInfo{CopyDirContentsOnly: true}), input.Opts...)
	return input.Worker.Run(
		llb.AddMount("/tmp/dalec/select-image-packages.sh", script, llb.SourcePath("select.sh"), llb.Readonly),
		llb.Args(append([]string{"/tmp/dalec/select-image-packages.sh"}, selected...)),
		dalec.WithConstraints(input.Opts...),
	).AddMount("/packages", repo)
}

func imageRepoMounts(input buildContainerInput) llb.RunOption {
	if input.ImagePackageRepo == nil {
		return dalec.WithRunOptions()
	}
	config := llb.Scratch().File(llb.Mkfile("dalec-images.list", 0o644,
		[]byte("deb [trusted=yes] file:/spec-package-repo ./\n")), input.Opts...)
	return dalec.WithRunOptions(
		llb.AddMount("/spec-package-repo", *input.ImagePackageRepo, llb.SourcePath("/repo"), llb.Readonly),
		llb.AddMount("/etc/apt/sources.list.d/dalec-images.list", config, llb.SourcePath("dalec-images.list"), llb.Readonly),
	)
}

func installSelectedImagePackages(input buildContainerInput) llb.RunOption {
	const scriptData = `#!/bin/sh
set -eu
` + aptProxyConfigScript + `
configure_apt_proxy
trap cleanup_apt_proxy EXIT
rm -f /var/lib/apt/lists/_*
apt update
apt install -y /spec-selected-packages/*.deb
`
	script := llb.Scratch().File(llb.Mkfile("install.sh", 0o755, []byte(scriptData)), input.Opts...)
	return dalec.WithRunOptions(
		imageRepoMounts(input),
		llb.AddMount("/spec-selected-packages", input.SpecPackages, llb.Readonly),
		llb.AddMount("/tmp/dalec/install-image-packages.sh", script, llb.SourcePath("install.sh"), llb.Readonly),
		llb.Args([]string{"/tmp/dalec/install-image-packages.sh"}),
		llb.AddEnv("DEBIAN_FRONTEND", "noninteractive"),
	)
}
