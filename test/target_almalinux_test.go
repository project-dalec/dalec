package test

import (
	"context"
	"testing"

	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/project-dalec/dalec/targets/linux/rpm/almalinux"
)

func TestDalecTargetAlmalinux9(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	cfg := testLinuxConfig{
		Target: targetConfig{
			Key:       "almalinux9",
			Package:   "almalinux9/rpm",
			Container: "almalinux9/container",
			DepsOnly:  "almalinux9/container/depsonly",
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
	}
	testLinuxDistro(ctx, t, cfg)
	testAlmalinuxExtra(ctx, t, cfg, almalinux.ConfigV9.ImageRef)
}

func TestDalecTargetAlmalinux8(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	cfg := testLinuxConfig{
		Target: targetConfig{
			Package:   "almalinux8/rpm",
			Container: "almalinux8/container",
			DepsOnly:  "almalinux8/container/depsonly",
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
	}
	testLinuxDistro(ctx, t, cfg)
	testAlmalinuxExtra(ctx, t, cfg, almalinux.ConfigV8.ImageRef)
}

func testAlmalinuxExtra(ctx context.Context, t *testing.T, cfg testLinuxConfig, distroImageRef string) {
	testSignedRPMCustomBaseImage(ctx, t, cfg.Target, distroImageRef)
}
