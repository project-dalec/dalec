package debian

import (
	"github.com/project-dalec/dalec/targets/linux/deb/distro"
)

const (
	BookwormDefaultTargetKey  = "bookworm"
	BookwormAptCachePrefix    = "bookworm"
	BookwormWorkerContextName = "dalec-bookworm-worker"

	bookwormRef       = "docker.io/library/debian:bookworm"
	bookwormVersionID = "debian12"
)

var (
	BookwormConfig = &distro.Config{
		ImageRef:           bookwormRef,
		AptCachePrefix:     BookwormAptCachePrefix,
		VersionID:          bookwormVersionID,
		ContextRef:         BookwormWorkerContextName,
		DefaultOutputImage: bookwormRef,
		BuilderPackages:    builderPackages,
		BasePackages:       basePackages,
	}
)
