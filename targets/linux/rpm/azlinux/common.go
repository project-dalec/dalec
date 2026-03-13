package azlinux

import (
	"github.com/project-dalec/dalec"
)

var (
	builderPackages = []string{
		"rpm-build",
		"mariner-rpm-macros",
		"build-essential",
		"ca-certificates",
	}

	defaultAzlinuxRepoPlatform = dalec.RepoPlatformConfig{
		ConfigRoot: "/etc/yum.repos.d",
		GPGKeyRoot: "/etc/pki/rpm-gpg",
		ConfigExt:  ".repo",
	}
)

func basePackages(name string) []dalec.Spec {
	const (
		distMin  = "distroless-packages-minimal"
		prebuilt = "prebuilt-ca-certificates"
		base     = "dalec-base-"
		license  = "Apache-2.0"
		version  = "0.0.1"
		rev      = "1"
	)

	return []dalec.Spec{
		{
			Name:        base + name,
			Version:     version,
			Revision:    rev,
			License:     license,
			Description: "DALEC base packages for " + name,
			Dependencies: &dalec.PackageDependencies{
				Runtime: dalec.PackageDependencyList{
					distMin: {},
				},
				Recommends: dalec.PackageDependencyList{
					prebuilt: {},
				},
			},
		},
	}
}
