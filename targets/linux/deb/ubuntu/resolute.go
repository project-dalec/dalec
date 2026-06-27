package ubuntu

import (
	"github.com/project-dalec/dalec/targets/linux/deb/distro"
)

const (
	ResoluteDefaultTargetKey  = "resolute"
	ResoluteAptCachePrefix    = "resolute"
	ResoluteWorkerContextName = "dalec-resolute-worker"

	resoluteRef       = "docker.io/library/ubuntu:resolute"
	resoluteVersionID = "ubuntu26.04"
)

var (
	ResoluteConfig = &distro.Config{
		ImageRef:           resoluteRef,
		AptCachePrefix:     ResoluteAptCachePrefix,
		VersionID:          resoluteVersionID,
		ContextRef:         ResoluteWorkerContextName,
		DefaultOutputImage: resoluteRef,
		BuilderPackages:    builderPackages,
		BasePackages:       basePackages,
		SysextSupported:    true,
	}
)
