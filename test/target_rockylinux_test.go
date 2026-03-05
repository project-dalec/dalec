package test

import (
	"testing"

	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/project-dalec/dalec/targets/linux/rpm/rockylinux"
)

func TestDalecTargetRockylinux9(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	testLinuxDistro(ctx, t, testLinuxConfig{
		Target: targetConfig{
			Key:       "rockylinux9",
			Package:   "rockylinux9/rpm",
			Container: "rockylinux9/container",
			Worker:    "rockylinux9/worker",
			FormatDepEqual: func(v, _ string) string {
				return v
			},
			ListExpectedSignFiles: azlinuxListSignFiles("el9"),
			PackageOverrides: map[string]string{
				"rust":  "rust cargo",
				"bazel": noPackageAvailable,
			},
		},
		LicenseDir: "/usr/share/licenses",
		SystemdDir: struct {
			Units   string
			Targets string
		}{
			Units:   "/usr/lib/systemd",
			Targets: "/etc/systemd/system",
		},
		Libdir: "/usr/lib64",
		Worker: workerConfig{
			ContextName:    rockylinux.ConfigV9.ContextRef,
			CreateRepo:     createYumRepo(rockylinux.ConfigV9),
			SignRepo:       signRepoDnf,
			TestRepoConfig: azlinuxTestRepoConfig,
		},
		Release: OSRelease{
			ID:        "rocky",
			VersionID: "9",
		},
		SupportsGomodVersionUpdate: true,
		Platforms: []ocispecs.Platform{
			{OS: "linux", Architecture: "amd64"},
			{OS: "linux", Architecture: "arm64"},
		},
		PackageOutputPath: rpmTargetOutputPath("el9"),
	})
}

func TestDalecTargetRockylinux8(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	testLinuxDistro(ctx, t, testLinuxConfig{
		Target: targetConfig{
			Package:   "rockylinux8/rpm",
			Container: "rockylinux8/container",
			Worker:    "rockylinux8/worker",
			FormatDepEqual: func(v, _ string) string {
				return v
			},
			ListExpectedSignFiles: azlinuxListSignFiles("el8"),
			PackageOverrides: map[string]string{
				"rust":  "rust cargo",
				"bazel": noPackageAvailable,
			},
		},
		LicenseDir: "/usr/share/licenses",
		SystemdDir: struct {
			Units   string
			Targets string
		}{
			Units:   "/usr/lib/systemd",
			Targets: "/etc/systemd/system",
		},
		Libdir: "/usr/lib64",
		Worker: workerConfig{
			ContextName:    rockylinux.ConfigV8.ContextRef,
			CreateRepo:     createYumRepo(rockylinux.ConfigV8),
			SignRepo:       signRepoDnf,
			TestRepoConfig: azlinuxTestRepoConfig,
		},
		Release: OSRelease{
			ID:        "rocky",
			VersionID: "8",
		},
		SupportsGomodVersionUpdate: true,
		Platforms: []ocispecs.Platform{
			{OS: "linux", Architecture: "amd64"},
			{OS: "linux", Architecture: "arm64"},
		},
		PackageOutputPath: rpmTargetOutputPath("el8"),
	})
}
