package suse

import (
	"github.com/project-dalec/dalec/packaging/linux/rpm"
	"github.com/project-dalec/dalec/targets/linux/rpm/distro"
)

const (
	// SLES15TargetKey is the target name recipes use, e.g. "sles15/rpm".
	SLES15TargetKey   = "sles15"
	zypperCacheSLES15 = "sles15-zypper-cache"

	// sles15Ref is the image ref used for the base worker image. Pin to 15.7:
	// SP6 (15.6) reached end of general support on 2025-12-31, so builds should
	// start from the currently-maintained 15.7 BCI (or an LTSS-backed source).
	sles15Ref      = "registry.suse.com/bci/bci-base:15.7"
	sles15FullName = "SUSE Linux Enterprise 15"
	// sles15WorkerContextName is the build context name that can be used to look
	// up a caller-provided worker image instead of building one from sles15Ref.
	sles15WorkerContextName = "dalec-sles15-worker"
)

// ConfigSLES15 is the dalec distro configuration for SUSE Linux Enterprise 15.
// It builds rpms inside a SUSE base image using zypper. Cross-architecture
// builds cannot use dnf's --forcearch/--installroot path because zypper lacks
// that mechanism, so dalec falls back to running the worker on the requested
// target platform (using binfmt/QEMU when available).
var ConfigSLES15 = &distro.Config{
	ImageRef:   sles15Ref,
	ContextRef: sles15WorkerContextName,

	CacheName: zypperCacheSLES15,
	CacheDir:  []string{"/var/cache/zypp"},

	ReleaseVer:                  "15",
	BuilderPackages:             builderPackages,
	BasePackages:                basePackages(SLES15TargetKey),
	RepoPlatformConfig:          &defaultPlatformConfig,
	InstallFunc:                 distro.ZypperInstall,
	CrossArchInstallUnsupported: true,
	FullName:                    sles15FullName,
	RPMMacros:                   sles15RPMMacros,
}

// sles15RPMMacros are the rpm macro overrides needed to make SUSE's rpm behave
// like the dnf-based distros dalec already supports, so the shared integration
// tests and the cross-distro file-layout contract hold on SLES 15.
var sles15RPMMacros = []rpm.SpecMacro{
	// SUSE's rpm does not define %{dist} (unlike the dnf-based distros, whose
	// base images set e.g. .el9 / .azl3), so %{?dist} would expand to nothing and
	// SUSE packages would be indistinguishable from other distros' rpms of the
	// same NVR. Define a distinct dist tag so the produced files (and their
	// Release field) are identifiable as SLES 15 builds.
	{Name: "dist", Value: ".sles15"},

	// SUSE defaults %{_docdir} to /usr/share/doc/packages, but dalec (and the
	// shared tests) expect the dnf layout /usr/share/doc/<name>.
	{Name: "_docdir", Value: "/usr/share/doc"},

	// SUSE's rpm does not define %{_licensedir} at all (it would expand to the
	// literal "%_licensedir"), so license artifacts would have nowhere to go.
	// Define it to /usr/share/licenses to match the dnf distros.
	{Name: "_licensedir", Value: "/usr/share/licenses"},

	// SUSE's default __os_install_post runs the brp-suse policy scripts, which
	// fail to operate on dalec's tmpfs --buildroot ("failed to open root
	// directory") and never strip binaries. Point it at the generic brp-strip
	// (the exact mechanism the dnf-based distros use in the same environment).
	//
	// This MUST use %define (Define: true), not %global. %global would expand
	// %{__strip} eagerly at definition time, baking in the default strip binary
	// before DisableStrip() can set it to /bin/true. %define defers %{__strip}
	// until __os_install_post actually runs, so the DisableStrip override still
	// takes effect when stripping is disabled.
	{
		Name:   "__os_install_post",
		Value:  "/usr/lib/rpm/brp-compress \\\n    /usr/lib/rpm/brp-strip %{__strip} \\\n%{nil}",
		Define: true,
	},
}
