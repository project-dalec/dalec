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
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
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
			// SUSE builds carry a custom .sles15 %{dist} tag so the produced rpms
			// are distinguishable from other distros' rpms of the same NVR.
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
		// openSUSE Leap / SLE 15 keep %{_libexecdir} at /usr/lib (only Tumbleweed
		// migrated to the FHS 3.0 /usr/libexec), so libexec artifacts land there.
		LibexecDir: "/usr/lib",
		Worker: workerConfig{
			ContextName:    suse.ConfigSLES15.ContextRef,
			BaseImageRef:   suse.ConfigSLES15.ImageRef,
			CreateRepo:     createZypperRepo(suse.ConfigSLES15),
			SignRepo:       signRepoZypper,
			TestRepoConfig: azlinuxTestRepoConfig,
		},
		Release: OSRelease{
			ID:        "sles",
			VersionID: "15.7",
		},
		SupportsGomodVersionUpdate: true,
	}
	testLinuxDistro(ctx, t, cfg)
	testSuseExtra(ctx, t, cfg, suse.ConfigSLES15.ImageRef)
	testSLESBasePackageBootstrap(ctx, t, cfg)
}

func testSuseExtra(ctx context.Context, t *testing.T, cfg testLinuxConfig, distroImageRef string) {
	testSignedRPMCustomBaseImage(ctx, t, cfg.Target, distroImageRef, true, cfg.Worker)
}

func testSLESBasePackageBootstrap(ctx context.Context, t *testing.T, cfg testLinuxConfig) {
	t.Run("base_packages_are_installed_before_application_dependencies", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)

		spec := testLinuxSpec(t, dalec.Spec{
			Dependencies: &dalec.PackageDependencies{
				Runtime: dalec.PackageDependencyList{
					"bash":        {},
					"coreutils":   {},
					"grep":        {},
					"libopenssl3": {},
					"moby-runc": {
						Version: []string{">= 1.1.0"},
					},
				},
				Recommends: dalec.PackageDependencyList{
					"git":  {},
					"pigz": {},
					"xz":   {},
				},
				ExtraRepos: []dalec.PackageRepositoryConfig{
					{
						// This intentionally mirrors the external repository and
						// dependency graph that exposed the combined-transaction
						// failure. Key rotation or repository changes may require
						// updating this fixture independently of Dalec.
						Keys: map[string]dalec.Source{
							"msft.asc": {
								HTTP: &dalec.SourceHTTP{
									URL:         "https://packages.microsoft.com/keys/microsoft.asc",
									Digest:      digest.Digest("sha256:2fa9c05d591a1582a9aba276272478c262e95ad00acf60eaee1644d93941e3c6"),
									Permissions: 0o644,
								},
							},
						},
						Config: map[string]dalec.Source{
							"microsoft-prod.repo": {
								Inline: &dalec.SourceInline{
									File: &dalec.SourceInlineFile{
										Contents: `[packages-microsoft-com-prod]
name=Microsoft Production
baseurl=https://packages.microsoft.com/sles/15/prod/
enabled=1
gpgcheck=1
repo_gpgcheck=1
gpgkey=file:///usr/share/pki/rpm-gpg/msft.asc
sslverify=1
`,
									},
								},
							},
						},
						Envs: []string{"install"},
					},
				},
			},
			Sources: map[string]dalec.Source{
				"service.service": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents: `[Unit]
Description=Dalec SLES bootstrap regression test

[Service]
Type=oneshot
ExecStart=/usr/bin/true

[Install]
WantedBy=multi-user.target
`,
						},
					},
				},
			},
			Artifacts: dalec.Artifacts{
				Systemd: &dalec.SystemdConfiguration{
					Units: map[string]dalec.SystemdUnitConfig{
						"service.service": {Enable: true},
					},
				},
			},
			Tests: []*dalec.TestSpec{
				{
					Name: "base and application packages installed",
					Files: map[string]dalec.FileCheckOutput{
						"/etc/os-release": {
							Permissions: 0o644,
						},
						filepath.Join(cfg.SystemdDir.Targets, "multi-user.target.wants/service.service"): {
							LinkTarget: "/usr/lib/systemd/system/service.service",
						},
					},
				},
			},
		})

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			req := newSolveRequest(
				withSpec(ctx, t, &spec),
				withBuildTarget(cfg.Target.Container),
				withIgnoreCache(frontend.IgnoreCacheTestsKey),
			)
			solveT(ctx, t, client, req)
		})
	})
}

// suseListSignFiles lists the rpm artifacts expected to be signed for SUSE.
// SUSE builds set a custom .sles15 %{dist} tag (see suse.ConfigSLES15), so the
// file names include a .sles15 dist component.
func suseListSignFiles(spec *dalec.Spec, platform ocispecs.Platform) []string {
	base := fmt.Sprintf("%s-%s-%s.sles15", spec.Name, spec.Version, spec.Revision)
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
