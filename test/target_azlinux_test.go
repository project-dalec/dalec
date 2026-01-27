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

func TestMariner2(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	cfg := testLinuxConfig{
		Target: targetConfig{
			Package:   "mariner2/rpm",
			Container: "mariner2/container",
			Worker:    "mariner2/worker",
			FormatDepEqual: func(v, _ string) string {
				return v
			},
			ListExpectedSignFiles: azlinuxListSignFiles("cm2"),
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
			ContextName:    azlinux.Mariner2WorkerContextName,
			CreateRepo:     createYumRepo(azlinux.Mariner2Config),
			SignRepo:       signRepoAzLinux,
			TestRepoConfig: azlinuxTestRepoConfig,
		},
		Release: OSRelease{
			ID:        "mariner",
			VersionID: "2.0",
		},
		Platforms: []ocispecs.Platform{
			{OS: "linux", Architecture: "amd64"},
			{OS: "linux", Architecture: "arm64"},
		},
		PackageOutputPath: rpmTargetOutputPath("cm2"),
	}

	testLinuxDistro(ctx, t, cfg)
	testAzlinuxExtra(ctx, t, cfg)
}

func TestAzlinux3(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	cfg := testLinuxConfig{
		Target: targetConfig{
			Key:                   "azlinux3",
			Package:               "azlinux3/rpm",
			Container:             "azlinux3/container",
			Worker:                "azlinux3/worker",
			Sysext:                "azlinux3/testing/sysext",
			ListExpectedSignFiles: azlinuxListSignFiles("azl3"),
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
			CreateRepo:     createYumRepo(azlinux.Azlinux3Config),
			SignRepo:       signRepoAzLinux,
			TestRepoConfig: azlinuxTestRepoConfig,
			SysextWorker:   azlinux.Azlinux3Config.SysextWorker,
		},
		Release: OSRelease{
			ID:        "azurelinux",
			VersionID: "3.0",
		},
		Platforms: []ocispecs.Platform{
			{OS: "linux", Architecture: "amd64"},
			{OS: "linux", Architecture: "arm64"},
		},
		PackageOutputPath: rpmTargetOutputPath("azl3"),
	}
	testLinuxDistro(ctx, t, cfg)
	testAzlinuxExtra(ctx, t, cfg)

	t.Run("ca-certs override", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)
		testAzlinuxCaCertsOverride(ctx, t, cfg.Target)
	})
}

func testAzlinuxExtra(ctx context.Context, t *testing.T, cfg testLinuxConfig) {

	t.Run("base deps", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)
		testAzlinuxBaseDeps(ctx, t, cfg.Target)
	})
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

func signRepoAzLinux(gpgKey llb.State, repoPath string) llb.StateOption {
	// key should be a state that has a public key under /public.key
	return func(in llb.State) llb.State {
		// For tdnf-based distros (Azlinux, Mariner), only sign repo metadata.
		// tdnf only verifies repo metadata signatures, not individual package signatures.
		// This is different from dnf which verifies both.

		scriptDt := `
#!/usr/bin/env bash

set -eux -o pipefail

gpg --import < /tmp/gpg/private.key
ID=$(gpg --list-keys --keyid-format LONG | grep -B 2 'test@example.com' | grep 'pub' | awk '{print $2}' | cut -d'/' -f2)

# For tdnf-based distros, only sign repo metadata, not individual packages
# tdnf only checks repo metadata signatures, not package signatures
# Signing packages can hang if rpmsign tries to prompt for passphrase

# Regenerate repo metadata
rm -rf ` + repoPath + `/repodata
createrepo --compatibility ` + repoPath + `

# Sign only the repo metadata
gpg --detach-sign --default-key "$ID" --armor --yes ` + repoPath + `/repodata/repomd.xml
`

		script := llb.Scratch().File(
			llb.Mkfile("/script.sh", 0o755, []byte(scriptDt)),
		)

		return in.Run(
			llb.AddMount("/tmp/signing", script, llb.Readonly),
			llb.AddMount("/tmp/gpg", gpgKey, llb.Readonly),
			dalec.ShArgs("/tmp/signing/script.sh"),
		).Root()
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

		script := llb.Scratch().File(
			llb.Mkfile("/script.sh", 0o755, []byte(scriptDt)),
		)

		return in.Run(
			llb.AddMount("/tmp/signing", script, llb.Readonly),
			llb.AddMount("/tmp/gpg", gpgKey, llb.Readonly),
			dalec.ShArgs("/tmp/signing/script.sh"),
		).Root()
	}
}

func testAzlinuxBaseDeps(ctx context.Context, t *testing.T, cfg targetConfig) {
	spec := newSimpleSpec()
	spec.Tests = []*dalec.TestSpec{
		{
			Name: "validate no shell",
			Files: map[string]dalec.FileCheckOutput{
				"/bin/sh/": {
					NotExist: true,
				},
				"/bin/bash/": {
					NotExist: true,
				},
				"/etc/pki": {
					IsDir: true,
				},
				"/etc/localtime": {},
			},
		},
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
		req := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Container))
		solveT(ctx, t, client, req)
	})
}
