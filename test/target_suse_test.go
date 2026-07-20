package test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/moby/buildkit/client/llb"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/targets/linux/rpm/distro"
	"github.com/project-dalec/dalec/targets/linux/rpm/suse"
)

func TestSLES15(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	cfg := testLinuxConfig{
		Target: targetConfig{
			Key:       "sles15",
			Package:   "sles15/rpm",
			Container: "sles15/container",
			DepsOnly:  "sles15/container/depsonly",
			Worker:    "sles15/worker",
			FormatDepEqual: func(v, _ string) string {
				return v
			},
			// SUSE rpms carry no %{?dist} tag, so sign-file names have no dist component.
			ListExpectedSignFiles: suseListSignFiles,
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
			ContextName:    suse.ConfigSLES15.ContextRef,
			CreateRepo:     createZypperRepo(suse.ConfigSLES15),
			SignRepo:       signRepoZypper,
			TestRepoConfig: azlinuxTestRepoConfig,
		},
		Release: OSRelease{
			ID:        "sles",
			VersionID: "15.7",
		},
		SupportsGomodVersionUpdate: true,
		// SUSE workers only support native-architecture builds
		// (CrossArchInstallUnsupported), so the cross-platform subtests
		// auto-skip when only the native platform is listed.
		Platforms: []ocispecs.Platform{
			{OS: "linux", Architecture: "amd64"},
		},
		PackageOutputPath: suseTargetOutputPath,
	}
	testLinuxDistro(ctx, t, cfg)
	testSuseExtra(ctx, t, cfg, suse.ConfigSLES15.ImageRef)
}

func testSuseExtra(ctx context.Context, t *testing.T, cfg testLinuxConfig, distroImageRef string) {
	testSignedRPMCustomBaseImage(ctx, t, cfg.Target, distroImageRef)
}

// suseTargetOutputPath produces the built-rpm path for SUSE. Unlike the
// el9/azl3 distros, SUSE rpms have an empty %{?dist} tag, so there is no dist
// component in the file name.
func suseTargetOutputPath(spec *dalec.Spec, platform ocispecs.Platform) string {
	arch := suseRpmArch(platform.Architecture)
	return fmt.Sprintf("/RPMS/%s/%s-%s-%s.%s.rpm", arch, spec.Name, spec.Version, spec.Revision, arch)
}

// suseListSignFiles lists the rpm artifacts expected to be signed for SUSE.
// SUSE has no %{?dist} tag, so the file names contain no dist component.
func suseListSignFiles(spec *dalec.Spec, platform ocispecs.Platform) []string {
	base := fmt.Sprintf("%s-%s-%s", spec.Name, spec.Version, spec.Revision)
	arch := suseRpmArch(platform.Architecture)

	return []string{
		filepath.Join("SRPMS", fmt.Sprintf("%s.src.rpm", base)),
		filepath.Join("RPMS", arch, fmt.Sprintf("%s.%s.rpm", base, arch)),
	}
}

func suseRpmArch(arch string) string {
	switch arch {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	default:
		return arch
	}
}

// createZypperRepo mirrors createYumRepo for zypper-based SUSE workers. It
// installs createrepo_c (SUSE has no bare "createrepo" package, but the
// createrepo_c package also provides a /usr/bin/createrepo compatibility
// symlink) and lays the local repo file under /etc/zypp/repos.d.
func createZypperRepo(installer *distro.Config) func(rpms llb.State, repoPath string, opts ...llb.StateOption) llb.StateOption {
	return func(rpms llb.State, repoPath string, opts ...llb.StateOption) llb.StateOption {
		return func(in llb.State) llb.State {
			suffixBytes := sha256.Sum256([]byte(repoPath))
			suffix := hex.EncodeToString(suffixBytes[:])[:8]
			localRepo := []byte(`
[Local-` + suffix + `]
name=Local Repository
baseurl=file://` + repoPath + `
gpgcheck=0
priority=0
enabled=1
metadata_expire=0
`)

			pg := dalec.ProgressGroup("Install local repo for test")

			installOpts := []distro.DnfInstallOpt{
				distro.DnfInstallWithConstraints([]llb.ConstraintsOpt{pg}),
			}

			withRepos := in.
				Run(installer.Install([]string{"createrepo_c"}, installOpts...), pg).
				File(llb.Mkdir(filepath.Join(repoPath, "RPMS"), 0o755, llb.WithParents(true)), pg).
				File(llb.Mkdir(filepath.Join(repoPath, "SRPMS"), 0o755), pg).
				File(llb.Mkfile("/etc/zypp/repos.d/local-"+suffix+".repo", 0o644, localRepo), pg).
				Run(
					llb.AddMount("/tmp/st", rpms, llb.Readonly),
					dalec.ShArgsf("cp /tmp/st/RPMS/$(uname -m)/* %s/RPMS/ && cp /tmp/st/SRPMS/* %s/SRPMS", repoPath, repoPath),
					pg,
				).
				Run(dalec.ShArgs("createrepo --compatibility "+repoPath),
					pg,
				).Root()

			for _, opt := range opts {
				withRepos = withRepos.With(opt)
			}

			return withRepos
		}
	}
}

// signRepoZypper mirrors signRepoDnf for SUSE. zypper verifies both package and
// repo-metadata signatures. SUSE provides rpmsign via the rpm-build package
// (there is no rpm-sign package), and createrepo via createrepo_c.
func signRepoZypper(gpgKey llb.State, repoPath string) llb.StateOption {
	// key should be a state that has a public key under /public.key
	return func(in llb.State) llb.State {
		scriptDt := `
#!/usr/bin/env bash

set -eux -o pipefail

if ! command -v rpmsign &> /dev/null; then
	zypper --non-interactive install rpm-build createrepo_c gpg2
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
