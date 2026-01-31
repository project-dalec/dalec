package test

import (
	"testing"

	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/project-dalec/dalec/targets/linux/rpm/almalinux"
)

func TestAlmalinux9(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	testLinuxDistro(ctx, t, testLinuxConfig{
		Target: targetConfig{
			Key:       "almalinux9",
			Package:   "almalinux9/rpm",
			Container: "almalinux9/container",
			Worker:    "almalinux9/worker",
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
			ContextName:    almalinux.ConfigV9.ContextRef,
			CreateRepo:     createYumRepo(almalinux.ConfigV9),
			SignRepo:       signRepoDnf,
			TestRepoConfig: azlinuxTestRepoConfig,
		},
		Release: OSRelease{
			ID:        "almalinux",
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

func TestAlmalinux8(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	testLinuxDistro(ctx, t, testLinuxConfig{
		Target: targetConfig{
			Package:   "almalinux8/rpm",
			Container: "almalinux8/container",
			Worker:    "almalinux8/worker",
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
			ContextName:    almalinux.ConfigV8.ContextRef,
			CreateRepo:     createYumRepo(almalinux.ConfigV8),
			SignRepo:       signRepoDnf,
			TestRepoConfig: azlinuxTestRepoConfig,
		},
		Release: OSRelease{
			ID:        "almalinux",
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
