package azlinux

import (
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/targets/linux/rpm/distro"
)

const (
	AzLinux4TargetKey    = "azlinux4"
	dnfCacheNameAzlinux4 = "azlinux4-dnf-cache"

	// Azlinux4Ref is the image ref used for the base worker image.
	//
	// TODO(azl4): Azure Linux 4 is currently published under the beta
	// channel. This location will change when it reaches general
	// availability.
	Azlinux4Ref      = "mcr.microsoft.com/azurelinux-beta/base/core:4.0"
	AzLinux4FullName = "Azure Linux 4"
	// Azlinux4WorkerContextName is the build context name that can be used to
	// override the base worker image.
	Azlinux4WorkerContextName = "dalec-azlinux4-worker"
)

// Azure Linux 4 is derived from Fedora and uses dnf5 (invoked as `dnf`).
// Some of its packages are differently named, and more aligned with
// Fedora. Also note that `build-essential` is not present as a meta-package,
// so we list individual build tool dependencies for consistency with
// Azure Linux 3.
var azlinux4BuilderPackages = []string{
	"rpm-build",

	// Core macros package.
	"azurelinux-rpm-config",

	// Provides systemd RPM macros (e.g. %{_unitdir}) used by specs that
	// install systemd units.
	"systemd-rpm-macros",

	// Build tools present in Azure Linux 3's build-essential package.
	"autoconf",
	"automake",
	"binutils",
	"bison",
	"diffutils",
	"file",
	"gawk",
	"gcc",
	"glibc-devel",
	"gzip",
	"kernel-headers",
	"libtool",
	"make",
	"patch",
	"pkgconf",
	"tar",

	"ca-certificates",
	"dnf5",
}

func azlinux4BasePackages() []dalec.Spec {
	const (
		base    = "dalec-base-"
		license = "Apache-2.0"
		version = "0.0.1"
		rev     = "1"
	)

	return []dalec.Spec{
		{
			Name:        base + AzLinux4TargetKey,
			Version:     version,
			Revision:    rev,
			License:     license,
			Description: "DALEC base packages for " + AzLinux4TargetKey,
			Dependencies: &dalec.PackageDependencies{
				Runtime: dalec.PackageDependencyList{
					"azurelinux-release": {},
					"tzdata":             {},
				},
			},
		},
	}
}

var Azlinux4Config = &distro.Config{
	ImageRef:   Azlinux4Ref,
	ContextRef: Azlinux4WorkerContextName,

	CacheName:        dnfCacheNameAzlinux4,
	CacheDir:         []string{"/var/cache/libdnf5"},
	CacheAddPlatform: true,

	ReleaseVer:         "4.0",
	BuilderPackages:    azlinux4BuilderPackages,
	BasePackages:       azlinux4BasePackages(),
	RepoPlatformConfig: &defaultAzlinuxRepoPlatform,
	InstallFunc:        distro.DnfInstall,

	SysextSupported: true,
	FullName:        AzLinux4FullName,
}
