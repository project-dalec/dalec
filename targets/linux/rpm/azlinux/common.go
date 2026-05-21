package azlinux

import (
	"github.com/project-dalec/dalec"
)

var (
	defaultAzlinuxRepoPlatform = dalec.RepoPlatformConfig{
		ConfigRoot: "/etc/yum.repos.d",
		GPGKeyRoot: "/etc/pki/rpm-gpg",
		ConfigExt:  ".repo",
	}
)
