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

					// Because dalec builds the SUSE container filesystem from
					// llb.Scratch() (there is no bci-base layer underneath), the
					// base image's minimal userland must be established here --
					// otherwise the rootfs contains only the transitive deps of
					// the packages being installed. This mirrors what azlinux
					// gets "for free" from the distro-provided
					// `distroless-packages-minimal` meta-package; SUSE ships no
					// such meta-package, so the equivalent minimal set is curated
					// explicitly.
					//
					// - system-user-{root,nobody}: create the standard /etc/passwd
					//   and /etc/group entries. Without these, artifact ownership
					//   (e.g. `User: nobody`) silently falls back to uid/gid 0 and
					//   `getent passwd nobody` returns nothing.
					// - coreutils/grep/gzip/gawk/sed: the core text-processing
					//   utilities every base Linux userland is expected to carry
					//   (on SUSE `grep`, `gzip`, `gawk`, `sed` are all standalone
					//   packages, not part of coreutils).
					"system-user-root":   {},
					"system-user-nobody": {},
					"coreutils":          {},
					"grep":               {},
					"gzip":               {},
					"gawk":               {},
					"sed":                {},
				},
			},
			// SUSE's public SLE_BCI repo publishes no installable
			// release/os-release package: the sole providers of the
			// `distribution-release` capability (e.g. `sles-release`) are
			// `@System`-only and cannot be installed into a fresh rootfs, so a
			// from-scratch install fails with
			// "nothing provides 'distribution-release' needed by aaa_base".
			// Provide the capability ourselves (and ship /etc/os-release) so
			// dalec can bootstrap a SUSE container filesystem from llb.Scratch()
			// without depending on a prebuilt base image.
			Provides: dalec.PackageDependencyList{
				"distribution-release": {},
				"sles-release":         {},
			},
			Sources: map[string]dalec.Source{
				"os-release": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Permissions: 0o644,
							Contents: `NAME="SLES"
VERSION="15-SP7"
VERSION_ID="15.7"
PRETTY_NAME="SUSE Linux Enterprise Server 15 SP7"
ID="sles"
ID_LIKE="suse"
ANSI_COLOR="0;32"
CPE_NAME="cpe:/o:suse:sles:15:sp7"
DOCUMENTATION_URL="https://documentation.suse.com/"
`,
						},
					},
				},
			},
			Artifacts: dalec.Artifacts{
				// Lands at /etc/os-release (%{_sysconfdir}/os-release).
				ConfigFiles: map[string]dalec.ArtifactConfig{
					"os-release": {},
				},
			},
		},
	}
}
