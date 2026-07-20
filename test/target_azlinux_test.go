package test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/targets/linux/rpm/azlinux"
)

var azlinuxTestRepoConfig = func(keyPath, repoPath string) map[string]dalec.Source {
	suffixBytes := sha256.Sum256([]byte(repoPath))
	suffix := hex.EncodeToString(suffixBytes[:])[:8]
	return map[string]dalec.Source{
		"local.repo": {
			Inline: &dalec.SourceInline{
				File: &dalec.SourceInlineFile{
					Contents: fmt.Sprintf(`[Local-%s]
name=Local Repository
baseurl=file://%s
repo_gpgcheck=1
priority=0
enabled=1
gpgkey=file:///etc/pki/rpm-gpg/%s
metadata_expire=0
	`, suffix, repoPath, keyPath),
				},
			},
		},
	}
}

func TestAzlinux3(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	cfg := testLinuxConfig{
		Target: targetConfig{
			Key:                   "azlinux3",
			Package:               "azlinux3/rpm",
			Container:             "azlinux3/container",
			DepsOnly:              "azlinux3/container/depsonly",
			Worker:                "azlinux3/worker",
			Sysext:                "azlinux3/testing/sysext",
			ListExpectedSignFiles: azlinuxListSignFiles("azl3"),
			Subpackages:           rpmSubpackageTests(),
		},
		LicenseDir: "/usr/share/licenses",
		SystemdDir: struct {
			Units   string
			Targets string
		}{
			Units:   "/usr/lib/systemd",
			Targets: "/etc/systemd/system",
		},
		Worker: workerConfig{
			ContextName:    azlinux.Azlinux3WorkerContextName,
			BaseImageRef:   azlinux.Azlinux3Config.ImageRef,
			CreateRepo:     createYumRepo(azlinux.Azlinux3Config),
			SignRepo:       signRepoDnf,
			TestRepoConfig: azlinuxTestRepoConfig,
			SysextWorker:   azlinux.Azlinux3Config.SysextWorker,
		},
		Release: OSRelease{
			ID:        "azurelinux",
			VersionID: "3.0",
		},
		SupportsGomodVersionUpdate: true,
	}
	testLinuxDistro(ctx, t, cfg)
	testAzlinuxExtra(ctx, t, cfg, azlinux.Azlinux3Config.ImageRef)

	t.Run("ca-certs override", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)
		testAzlinuxCaCertsOverride(ctx, t, cfg.Target)
	})
}

func TestAzlinux4(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	cfg := testLinuxConfig{
		Target: targetConfig{
			Key:                   "azlinux4",
			Package:               "azlinux4/rpm",
			Container:             "azlinux4/container",
			DepsOnly:              "azlinux4/container/depsonly",
			Worker:                "azlinux4/worker",
			Sysext:                "azlinux4/testing/sysext",
			ListExpectedSignFiles: azlinuxListSignFiles("azl4"),
			Subpackages:           rpmSubpackageTests(),
			PackageOverrides: map[string]string{
				// NOTE: bazel is not presently available in azl4 base repos.
				"bazel": noPackageAvailable,
				// On azl4 (Fedora-derived), `cargo` is a separate package
				// from `rust`; install both for tests that need cargo.
				"rust": "rust cargo",
			},
		},
		Libdir:     "/usr/lib64",
		LicenseDir: "/usr/share/licenses",
		SystemdDir: struct {
			Units   string
			Targets string
		}{
			Units:   "/usr/lib/systemd",
			Targets: "/etc/systemd/system",
		},
		Worker: workerConfig{
			ContextName:    azlinux.Azlinux4WorkerContextName,
			CreateRepo:     createYumRepo(azlinux.Azlinux4Config),
			SignRepo:       signRepoDnf,
			TestRepoConfig: azlinuxTestRepoConfig,
			SysextWorker:   azlinux.Azlinux4Config.SysextWorker,
		},
		Release: OSRelease{
			ID:        "azurelinux",
			VersionID: "4.0",
		},
		SupportsGomodVersionUpdate: true,
	}
	testLinuxDistro(ctx, t, cfg)
	testAzlinuxExtra(ctx, t, cfg, azlinux.Azlinux4Config.ImageRef)

	t.Run("ca-certs override", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)
		testAzlinuxCaCertsOverride(ctx, t, cfg.Target)
	})
}

func testAzlinuxExtra(ctx context.Context, t *testing.T, cfg testLinuxConfig, distroImageRef string) {
	t.Run("base deps", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)
		testAzlinuxBaseDeps(ctx, t, cfg.Target)
	})

	testSignedRPMCustomBaseImage(ctx, t, cfg.Target, distroImageRef)
}

func testAzlinuxCaCertsOverride(ctx context.Context, t *testing.T, target targetConfig) {
	spec := newSimpleSpec()
	spec.Dependencies = &dalec.PackageDependencies{
		Runtime: map[string]dalec.PackageConstraints{
			"ca-certificates": {},
		},
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
		req := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(target.Container))
		solveT(ctx, t, client, req)
	})
}

func azlinuxListSignFiles(ver string) func(*dalec.Spec, ocispecs.Platform) []string {
	return func(spec *dalec.Spec, platform ocispecs.Platform) []string {
		base := fmt.Sprintf("%s-%s-%s.%s", spec.Name, spec.Version, spec.Revision, ver)

		var arch string
		switch platform.Architecture {
		case "amd64":
			arch = "x86_64"
		case "arm64":
			arch = "aarch64"
		default:
			arch = platform.Architecture
		}

		return []string{
			filepath.Join("SRPMS", fmt.Sprintf("%s.src.rpm", base)),
			filepath.Join("RPMS", arch, fmt.Sprintf("%s.%s.rpm", base, arch)),
		}
	}
}

func signRepoDnf(gpgKey llb.State, repoPath string) llb.StateOption {
	// key should be a state that has a public key under /public.key
	return func(in llb.State) llb.State {
		// For dnf-based distros, sign both packages and repo metadata.
		// dnf verifies package signatures in addition to repo metadata signatures.

		scriptDt := `
#!/usr/bin/env bash

set -eux -o pipefail

if ! command -v rpm-sign &> /dev/null; then
	dnf install -y rpm-sign
fi

gpg --import < /tmp/gpg/private.key
ID=$(gpg --list-keys --keyid-format LONG | grep -B 2 'test@example.com' | grep 'pub' | awk '{print $2}' | cut -d'/' -f2)

echo "%_gpg_name $ID" > ~/.rpmmacros
find ` + repoPath + `/RPMS -name "*.rpm" -exec rpmsign --addsign {} \;

# Regenerate (and sign) repo metadata
rm -rf ` + repoPath + `/repodata
createrepo --compatibility ` + repoPath + `
gpg --detach-sign --default-key "$ID" --armor --yes ` + repoPath + `/repodata/repomd.xml
`

		pg := dalec.ProgressGroup("in-signing-script")

		script := llb.Scratch().File(
			llb.Mkfile("/script.sh", 0o755, []byte(scriptDt)),
			pg,
		)

		return in.Run(
			llb.AddMount("/tmp/signing", script, llb.Readonly),
			llb.AddMount("/tmp/gpg", gpgKey, llb.Readonly),
			dalec.ShArgs("/tmp/signing/script.sh"),
			pg,
		).Root()
	}
}

func testAzlinuxBaseDeps(ctx context.Context, t *testing.T, cfg targetConfig) {
	spec := newSimpleSpec()
	files := map[string]dalec.FileCheckOutput{
		"/bin/sh/": {
			NotExist: true,
		},
		"/bin/bash/": {
			NotExist: true,
		},
		"/etc/pki": {
			IsDir: true,
		},
	}

	// TODO(azl4): Azure Linux 4 doesn't presently materialize /etc/localtime
	// in the base image. (Azure Linux 3 does via the `distroless-packages-minimal`
	// package.)
	if cfg.Key == azlinux.AzLinux3TargetKey {
		files["/etc/localtime"] = dalec.FileCheckOutput{}
	}

	spec.Tests = []*dalec.TestSpec{
		{
			Name:  "validate no shell",
			Files: files,
		},
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
		req := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Container))
		solveT(ctx, t, client, req)
	})
}
