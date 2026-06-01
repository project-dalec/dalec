package almalinux

import (
	"github.com/project-dalec/dalec/targets/linux/rpm/distro"
)

const (
	V8TargetKey    = "almalinux8"
	dnfCacheNameV8 = "almalinux8-dnf-cache"

	// v8Ref is the image ref used for the base worker image
	v8Ref      = "docker.io/library/almalinux:8"
	v8FullName = "AlmaLinux 8"
	// v8WorkerContextName is the build context name that can be used to lookup
	v8WorkerContextName = "dalec-almalinux8-worker"
)

var ConfigV8 = &distro.Config{
	ImageRef:   v8Ref,
	ContextRef: v8WorkerContextName,

	CacheName: dnfCacheNameV8,
	CacheDir:  []string{"/var/cache/dnf"},
	// Alma's repo configs do not include the $basearch variable in the mirrorlist URL
	// This means that the cache key that dnf computes for /var/cache/dnf/<repoid>-<hash>
	// is the same across x86_64 and aarch64, which leads to incorrect repo metadata
	// when compiling for a non-native architecture.
	CacheAddPlatform: true,

	ReleaseVer:         "8",
	BuilderPackages:    builderPackages,
	BasePackages:       basePackages(V8TargetKey),
	RepoPlatformConfig: &defaultPlatformConfig,
	InstallFunc:        distro.DnfInstall,
	FullName:           v8FullName,
}
