package ubuntu

import (
	"github.com/project-dalec/dalec/targets/linux/deb/distro"
)

const (
	BionicDefaultTargetKey  = "bionic"
	BionicAptCachePrefix    = "bionic"
	BionicWorkerContextName = "dalec-bionic-worker"

	bionicRef       = "docker.io/library/ubuntu:bionic"
	bionicVersionID = "ubuntu18.04"
)

var (
	BionicConfig = &distro.Config{
		ImageRef:           bionicRef,
		AptCachePrefix:     BionicAptCachePrefix,
		VersionID:          bionicVersionID,
		ContextRef:         BionicWorkerContextName,
		DefaultOutputImage: bionicRef,
		BuilderPackages:    builderPackages,
		BasePackages:       basePackages,
	}
)
