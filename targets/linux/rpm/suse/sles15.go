package suse

import (
	"github.com/project-dalec/dalec/targets/linux/rpm/distro"
)

const (
	// SLES15TargetKey is the target name recipes use, e.g. "sles15/rpm".
	SLES15TargetKey   = "sles15"
	zypperCacheSLES15 = "sles15-zypper-cache"

	// sles15Ref is the image ref used for the base worker image.
	sles15Ref      = "registry.suse.com/bci/bci-base:15.6"
	sles15FullName = "SUSE Linux Enterprise 15"
	// sles15WorkerContextName is the build context name that can be used to look
	// up a caller-provided worker image instead of building one from sles15Ref.
	sles15WorkerContextName = "dalec-sles15-worker"
)

// ConfigSLES15 is the dalec distro configuration for SUSE Linux Enterprise 15.
// It builds rpms inside a SUSE base image using zypper. Cross-architecture
// builds are not supported because zypper lacks dnf's --forcearch/--installroot
// mechanism.
var ConfigSLES15 = &distro.Config{
	ImageRef:   sles15Ref,
	ContextRef: sles15WorkerContextName,

	CacheName: zypperCacheSLES15,
	CacheDir:  []string{"/var/cache/zypp"},

	ReleaseVer:                  "15",
	BuilderPackages:             builderPackages,
	BasePackages:                basePackages(SLES15TargetKey),
	RepoPlatformConfig:          &defaultPlatformConfig,
	InstallFunc:                 distro.ZypperInstall,
	CrossArchInstallUnsupported: true,
	FullName:                    sles15FullName,
}
