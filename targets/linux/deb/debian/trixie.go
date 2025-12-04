package debian

import (
	"github.com/project-dalec/dalec/targets/linux/deb/distro"
)

const (
	TrixieDefaultTargetKey  = "trixie"
	TrixieAptCachePrefix    = "trixie"
	TrixieWorkerContextName = "dalec-trixie-worker"

	trixieRef       = "docker.io/library/debian:trixie"
	trixieVersionID = "debian13"
)

var (
	TrixieConfig = &distro.Config{
		ImageRef:           trixieRef,
		AptCachePrefix:     TrixieAptCachePrefix,
		VersionID:          trixieVersionID,
		ContextRef:         TrixieWorkerContextName,
		DefaultOutputImage: trixieRef,
		BuilderPackages:    builderPackages,
		BasePackages:       basePackages,
		SysextSupported:    true,
	}
)
