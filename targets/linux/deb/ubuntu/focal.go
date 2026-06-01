package ubuntu

import (
	"github.com/project-dalec/dalec/targets/linux/deb/distro"
)

const (
	FocalDefaultTargetKey  = "focal"
	FocalAptCachePrefix    = "focal"
	FocalWorkerContextName = "dalec-focal-worker"

	focalRef       = "docker.io/library/ubuntu:focal"
	focalVersionID = "ubuntu20.04"
)

var (
	FocalConfig = &distro.Config{
		ImageRef:           focalRef,
		AptCachePrefix:     FocalAptCachePrefix,
		VersionID:          focalVersionID,
		ContextRef:         FocalWorkerContextName,
		DefaultOutputImage: focalRef,
		BuilderPackages:    builderPackages,
		BasePackages:       basePackages,
	}
)
