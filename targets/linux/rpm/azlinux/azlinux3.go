package azlinux

import (
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/targets/linux/rpm/distro"
)

const (
	AzLinux3TargetKey     = "azlinux3"
	tdnfCacheNameAzlinux3 = "azlinux3-tdnf-cache"

	// Azlinux3Ref is the image ref used for the base worker image
	Azlinux3Ref      = "mcr.microsoft.com/azurelinux/base/core:3.0"
	AzLinux3FullName = "Azure Linux 3"
	// Azlinux3WorkerContextName is the build context name that can be used to lookup
	Azlinux3WorkerContextName = "dalec-azlinux3-worker"
)

// Azure Linux 3 is CBL-Mariner-derived and uses tdnf as its package
// manager. Its `mariner-rpm-macros` package is the canonical source for
// `%{_libdir}` overrides, systemd unit dirs, etc., and `build-essential`
// is a meta-package providing the standard build tools.
var azlinux3BuilderPackages = []string{
	"rpm-build",
	"mariner-rpm-macros",
	"build-essential",
	"ca-certificates",
	"dnf",
}

func azlinux3BasePackages() []dalec.Spec {
	const (
		distMin  = "distroless-packages-minimal"
		prebuilt = "prebuilt-ca-certificates"
		base     = "dalec-base-"
		license  = "Apache-2.0"
		version  = "0.0.1"
		rev      = "1"
	)

	return []dalec.Spec{
		{
			Name:        base + AzLinux3TargetKey,
			Version:     version,
			Revision:    rev,
			License:     license,
			Description: "DALEC base packages for " + AzLinux3TargetKey,
			Dependencies: &dalec.PackageDependencies{
				Runtime: dalec.PackageDependencyList{
					distMin: {},
				},
				Recommends: dalec.PackageDependencyList{
					prebuilt: {},
				},
			},
		},
	}
}

var Azlinux3Config = &distro.Config{
	ImageRef:   Azlinux3Ref,
	ContextRef: Azlinux3WorkerContextName,

	CacheName:        tdnfCacheNameAzlinux3,
	CacheDir:         []string{"/var/cache/tdnf", "/var/cache/dnf"},
	CacheAddPlatform: true,

	ReleaseVer:         "3.0",
	BuilderPackages:    azlinux3BuilderPackages,
	BasePackages:       azlinux3BasePackages(),
	RepoPlatformConfig: &defaultAzlinuxRepoPlatform,
	InstallFunc:        distro.TdnfInstall,

	SysextSupported: true,
	FullName:        AzLinux3FullName,
}
