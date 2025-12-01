package almalinux

import (
	"github.com/project-dalec/dalec/targets/linux/rpm/distro"
)

const (
	V9TargetKey    = "almalinux9"
	dnfCacheNameV9 = "almalinux9-dnf-cache"

	// v9Ref is the image ref used for the base worker image
	v9Ref      = "docker.io/library/almalinux:9"
	v9FullName = "AlmaLinux 9"
	// v9WorkerContextName is the build context name that can be used to lookup
	v9WorkerContextName = "dalec-almalinux9-worker"
)

var ConfigV9 = &distro.Config{
	ImageRef:   v9Ref,
	ContextRef: v9WorkerContextName,

	CacheName: dnfCacheNameV9,
	CacheDir:  "/var/cache/dnf",
	// Alma's repo configs do not include the $basearch variable in the mirrorlist URL
	// This means that the cache key that dnf computes for /var/cache/dnf/<repoid>-<hash>
	// is the same across x86_64 and aarch64, which leads to incorrect repo metadata
	// when compiling for a non-native architecture.
	CacheAddPlatform: true,

	ReleaseVer:         "9",
	BuilderPackages:    builderPackages,
	BasePackages:       basePackages(V9TargetKey),
	RepoPlatformConfig: &defaultPlatformConfig,
	InstallFunc:        distro.DnfInstall,
	FullName:           v9FullName,
}
