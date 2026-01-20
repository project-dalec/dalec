package azlinux

import (
	"github.com/project-dalec/dalec/targets/linux/rpm/distro"
)

const (
	Mariner2TargetKey     = "mariner2"
	tdnfCacheNameMariner2 = "mariner2-tdnf-cache"

	Mariner2Ref               = "mcr.microsoft.com/cbl-mariner/base/core:2.0"
	Mariner2FullName          = "CBL-Mariner 2"
	Mariner2WorkerContextName = "dalec-mariner2-worker"
)

var Mariner2Config = &distro.Config{
	ImageRef:   "mcr.microsoft.com/cbl-mariner/base/core:2.0",
	ContextRef: Mariner2WorkerContextName,

	CacheName:        tdnfCacheNameMariner2,
	CacheDir:         []string{"/var/cache/tdnf", "/var/cache/dnf"},
	CacheAddPlatform: true,

	ReleaseVer:         "2.0",
	BuilderPackages:    append(builderPackages, "dnf"),
	BasePackages:       basePackages(Mariner2TargetKey),
	RepoPlatformConfig: &defaultAzlinuxRepoPlatform,
	InstallFunc:        distro.TdnfInstall,
	FullName:           Mariner2FullName,
}
