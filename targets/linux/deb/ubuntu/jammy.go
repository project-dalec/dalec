package ubuntu

import (
	"github.com/project-dalec/dalec/targets/linux/deb/distro"
)

const (
	JammyDefaultTargetKey  = "jammy"
	JammyAptCachePrefix    = "jammy"
	JammyWorkerContextName = "dalec-jammy-worker"

	jammyRef       = "docker.io/library/ubuntu:jammy"
	JammyVersionID = "ubuntu22.04"
)

var (
	JammyConfig = &distro.Config{
		ImageRef:           jammyRef,
		AptCachePrefix:     JammyAptCachePrefix,
		VersionID:          JammyVersionID,
		ContextRef:         JammyWorkerContextName,
		DefaultOutputImage: jammyRef,
		BuilderPackages:    builderPackages,
		BasePackages:       basePackages,
	}
)
