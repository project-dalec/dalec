package test

import (
	"context"
	"fmt"
	"testing"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/targets/linux/deb/distro"
	"github.com/project-dalec/dalec/targets/linux/deb/ubuntu"
)

func withPackageOverride(oldPkg, newPkg string) func(cfg *testLinuxConfig) {
	return func(cfg *testLinuxConfig) {
		if cfg.Target.PackageOverrides == nil {
			cfg.Target.PackageOverrides = make(map[string]string)
		}

		cfg.Target.PackageOverrides[oldPkg] = newPkg
	}
}

func debLinuxTestConfigFor(targetKey string, cfg *distro.Config, opts ...func(*testLinuxConfig)) testLinuxConfig {
	var sysextTarget string
	if cfg.SysextSupported {
		sysextTarget = targetKey + "/testing/sysext"
	}

	tlc := testLinuxConfig{
		Target: targetConfig{
			Key:       targetKey,
			Container: targetKey + "/testing/container",
			Package:   targetKey + "/deb",
			Worker:    targetKey + "/worker",
			Sysext:    sysextTarget,
			FormatDepEqual: func(ver, rev string) string {
				return ver + "-" + cfg.VersionID + "u" + rev
			},
			ListExpectedSignFiles: debExpectedFiles(cfg.VersionID),
		},
		LicenseDir: "/usr/share/doc",
		SystemdDir: struct {
			Units   string
			Targets string
		}{
			Units:   "/lib/systemd",
			Targets: "/etc/systemd/system",
		},
		Worker: workerConfig{
			ContextName: cfg.ContextRef,
			// /pkg1.deb ...
			CreateRepo:     ubuntuCreateRepo(cfg),
			SignRepo:       signRepoUbuntu,
			TestRepoConfig: ubuntuTestRepoConfig,
			SysextWorker:   cfg.SysextWorker,
		},

		Platforms: []ocispecs.Platform{
			{OS: "linux", Architecture: "amd64"},
			{OS: "linux", Architecture: "arm64"},
			{OS: "linux", Architecture: "arm", Variant: "v7"},
		},
		PackageOutputPath: debTargetOutputPath(cfg.VersionID),
	}

	for _, o := range opts {
		o(&tlc)
	}
	return tlc
}

func ubuntuCreateRepo(cfg *distro.Config) func(pkg llb.State, repoPath string, opts ...llb.StateOption) llb.StateOption {
	return func(pkg llb.State, repoPath string, opts ...llb.StateOption) llb.StateOption {
		repoFile := []byte(`
deb [trusted=yes] copy:` + repoPath + `/ /
`)

		c := dalec.ProgressGroup("Creating Ubuntu repo")

		return func(in llb.State) llb.State {
			withRepo := in.Run(
				dalec.ShArgs("apt-get update && apt-get install -y apt-utils gnupg2"),
				dalec.WithMountedAptCache(cfg.AptCachePrefix, c),
				c,
			).File(llb.Copy(pkg, "/", repoPath, dalec.WithCreateDestPath()), c).
				Run(
					llb.Dir(repoPath),
					dalec.ShArgs("apt-ftparchive packages . > Packages"),
					c,
				).
				Run(
					llb.Dir(repoPath),
					dalec.ShArgs("apt-ftparchive release . > Release"),
					c,
				).Root()

			for _, opt := range opts {
				withRepo = opt(withRepo)
			}

			return withRepo.
				File(llb.Mkfile("/etc/apt/sources.list.d/test-dalec-local-repo.list", 0o644, repoFile), c)
		}
	}
}

func signRepoUbuntu(gpgKey llb.State, repoPath string) llb.StateOption {
	// key should be a state that has a public key under /public.key
	return func(in llb.State) llb.State {
		// assuming in is the state that has the repo files under / including
		// Release file
		return in.Run(
			dalec.ShArgs("gpg --import < /tmp/gpg/private.key"),
			llb.AddMount("/tmp/gpg", gpgKey, llb.Readonly),
			dalec.ProgressGroup("Importing gpg key")).
			Run(
				dalec.ShArgs(`ID=$(gpg --list-keys --keyid-format LONG | grep -B 2 'test@example.com' | grep 'pub' | awk '{print $2}' | cut -d'/' -f2) && \
					gpg --list-keys --keyid-format LONG && \
					gpg --default-key $ID -abs -o `+repoPath+`/Release.gpg `+repoPath+`/Release && \
					gpg --default-key "$ID" --clearsign -o `+repoPath+`/InRelease `+repoPath+`/Release`),
				llb.AddMount("/tmp/gpg", gpgKey, llb.Readonly),
				dalec.ProgressGroup("signing repo"),
			).Root()
	}
}

func ubuntuTestRepoConfig(name, repoPath string) map[string]dalec.Source {
	return map[string]dalec.Source{
		"local.list": {
			Inline: &dalec.SourceInline{
				File: &dalec.SourceInlineFile{
					Contents: fmt.Sprintf(`deb [signed-by=/usr/share/keyrings/%s] copy:`+repoPath+`/ /`, name),
				},
			},
		},
	}
}

func debExpectedFiles(ver string) func(*dalec.Spec, ocispecs.Platform) []string {
	return func(spec *dalec.Spec, platform ocispecs.Platform) []string {
		base := fmt.Sprintf("%s_%s-%su%s", spec.Name, spec.Version, ver, spec.Revision)
		out := []string{
			fmt.Sprintf("%s_%s.deb", base, platform.Architecture),
		}
		return out
	}
}

func TestJammy(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	testConf := debLinuxTestConfigFor(ubuntu.JammyDefaultTargetKey, ubuntu.JammyConfig,
		withPackageOverride("rust", "rust-all"),
		withPackageOverride("bazel", noPackageAvailable),
		withPackageOverride("python", "python3 python3-pip"),
	)

	testLinuxDistro(ctx, t, testConf)
	testUbuntuBaseDependencies(t, testConf.Target)
}

func TestNoble(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	testConf := debLinuxTestConfigFor(ubuntu.NobleDefaultTargetKey, ubuntu.NobleConfig,
		withPackageOverride("rust", "rust-all"),
		withPackageOverride("bazel", "bazel-bootstrap"),
	)
	testLinuxDistro(ctx, t, testConf)
	testUbuntuBaseDependencies(t, testConf.Target)
}

func TestFocal(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	testConf := debLinuxTestConfigFor(ubuntu.FocalDefaultTargetKey, ubuntu.FocalConfig,
		withPackageOverride("golang", "golang-1.22"),
		withPackageOverride("rust", "rust-all"),
		withPackageOverride("bazel", noPackageAvailable),
	)

	testLinuxDistro(ctx, t, testConf)
	testUbuntuBaseDependencies(t, testConf.Target)
}

func TestBionic(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	testConf := debLinuxTestConfigFor(ubuntu.BionicDefaultTargetKey, ubuntu.BionicConfig,
		withPackageOverride("golang", "golang-1.18"),
		withPackageOverride("rust", "rust-all"),
		withPackageOverride("bazel", noPackageAvailable),
	)

	testLinuxDistro(ctx, t, testConf)
	testUbuntuBaseDependencies(t, testConf.Target)
}

func testUbuntuBaseDependencies(t *testing.T, target targetConfig) {
	t.Run("base deps", func(t *testing.T) {
		t.Parallel()

		ctx := startTestSpan(baseCtx, t)
		spec := newSimpleSpec()
		spec.Tests = []*dalec.TestSpec{
			{
				Files: map[string]dalec.FileCheckOutput{
					"/etc/ssl/certs": {
						Permissions: 0755,
						IsDir:       true,
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			req := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(target.Container))
			solveT(ctx, t, client, req)
		})
	})
}
