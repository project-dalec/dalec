package azlinux

import (
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

var Azlinux3Config = &distro.Config{
	ImageRef:   Azlinux3Ref,
	ContextRef: Azlinux3WorkerContextName,

	CacheName:        tdnfCacheNameAzlinux3,
	CacheDir:         []string{"/var/cache/tdnf", "/var/cache/dnf"},
	CacheAddPlatform: true,

	ReleaseVer:         "3.0",
	BuilderPackages:    append(builderPackages, "dnf"),
	BasePackages:       basePackages(AzLinux3TargetKey),
	RepoPlatformConfig: &defaultAzlinuxRepoPlatform,
	InstallFunc:        distro.TdnfInstall,

	SysextSupported: true,
	FullName:        AzLinux3FullName,
}
