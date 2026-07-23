package suse

import (
	"github.com/project-dalec/dalec"
)

// builderPackages are the packages needed inside the worker image to build an
// rpm on SUSE. rpm-build provides rpmbuild; the rest are the standard build
// toolchain. Individual specs add their own build dependencies on top of these.
var builderPackages = []string{
	"rpm-build",
	"binutils",
	"gcc",
	"make",
	"tar",
	"gzip",
	"ca-certificates",
}

// defaultPlatformConfig tells dalec where zypper expects repository
// configuration and signing keys to live. zypper reads repos from
// /etc/zypp/repos.d (not /etc/yum.repos.d like dnf).
var defaultPlatformConfig = dalec.RepoPlatformConfig{
	ConfigRoot: "/etc/zypp/repos.d",
	GPGKeyRoot: "/etc/pki/rpm-gpg",
	ConfigExt:  ".repo",
}

func basePackages(name string) []dalec.Spec {
	const (
		base    = "dalec-base-"
		license = "Apache-2.0"

		version = "0.0.1"
		rev     = "1"
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
					// SUSE/openSUSE ship the tz database as "timezone"; there is
					// no "tzdata" package in the SLE_BCI repos (unlike Fedora/RHEL).
					"timezone": {},
				},
			},
		},
	}
}
