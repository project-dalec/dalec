package rockylinux

import (
	"github.com/project-dalec/dalec/targets/linux/rpm/distro"
)

const (
	V9TargetKey    = "rockylinux9"
	dnfCacheNameV9 = "rockylinux9-dnf-cache"

	// v9Ref is the image ref used for the base worker image
	v9Ref      = "docker.io/library/rockylinux:9"
	v9FullName = "RockyLinux 9"
	// v9WorkerContextName is the build context name that can be used to lookup
	v9WorkerContextName = "dalec-rockylinux9-worker"
)

var ConfigV9 = &distro.Config{
	ImageRef:   v9Ref,
	ContextRef: v9WorkerContextName,

	CacheName: dnfCacheNameV9,
	CacheDir:  "/var/cache/dnf",

	ReleaseVer:         "9",
	BuilderPackages:    append(builderPackages, "systemd-rpm-macros"),
	BasePackages:       basePackages(V9TargetKey),
	RepoPlatformConfig: &defaultPlatformConfig,
	InstallFunc:        distro.DnfInstall,
	FullName:           v9FullName,
}
