package test

import (
	"context"
	"strings"
	"testing"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/project-dalec/dalec"
)

// subpackageSpec returns a spec that produces a primary package and one
// subpackage ("contrib"). The primary package installs "primary-bin" to
// /usr/bin and the subpackage installs "contrib-bin" to /usr/bin.
func subpackageSpec(targetKey string) *dalec.Spec {
	return &dalec.Spec{
		Name:        "test-subpkgs",
		Version:     "0.0.1",
		Revision:    "1",
		Description: "Test subpackages end-to-end",
		License:     "MIT",
		Website:     "https://github.com/project-dalec/dalec",
		Packager:    "test",
		Sources: map[string]dalec.Source{
			"primary-bin": {
				Inline: &dalec.SourceInline{
					File: &dalec.SourceInlineFile{
						Contents:    "#!/usr/bin/env bash\necho primary",
						Permissions: 0o755,
					},
				},
			},
			"contrib-bin": {
				Inline: &dalec.SourceInline{
					File: &dalec.SourceInlineFile{
						Contents:    "#!/usr/bin/env bash\necho contrib",
						Permissions: 0o755,
					},
				},
			},
		},
		Build: dalec.ArtifactBuild{
			Steps: []dalec.BuildStep{
				{Command: "/bin/true"},
			},
		},
		Artifacts: dalec.Artifacts{
			Binaries: map[string]dalec.ArtifactConfig{
				"primary-bin": {},
			},
		},
		Targets: map[string]dalec.Target{
			targetKey: {
				Packages: map[string]dalec.SubPackage{
					"contrib": {
						Description: "Contributed extras for test-subpkgs",
						Artifacts: &dalec.Artifacts{
							Binaries: map[string]dalec.ArtifactConfig{
								"contrib-bin": {},
							},
						},
					},
				},
			},
		},
	}
}

// testSubpackages tests that supplemental packages (subpackages) are correctly
// built and installed into the container alongside the primary package.
func testSubpackages(ctx context.Context, t *testing.T, targetCfg targetConfig) {
	t.Run("primary and subpackage binaries are installed", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)

		spec := subpackageSpec(targetCfg.Key)
		// Add tests that verify both binaries are present in the container.
		spec.Tests = []*dalec.TestSpec{
			{
				Name: "both binaries installed",
				Files: map[string]dalec.FileCheckOutput{
					"/usr/bin/primary-bin": {},
					"/usr/bin/contrib-bin": {},
				},
				Steps: []dalec.TestStep{
					{
						Command: "/usr/bin/primary-bin",
						Stdout:  dalec.CheckOutput{Equals: "primary\n"},
						Stderr:  dalec.CheckOutput{Empty: true},
					},
					{
						Command: "/usr/bin/contrib-bin",
						Stdout:  dalec.CheckOutput{Equals: "contrib\n"},
						Stderr:  dalec.CheckOutput{Empty: true},
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
			sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(targetCfg.Container))
			solveT(ctx, t, gwc, sr)
		})
	})

	t.Run("subpackage with custom name", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)

		spec := subpackageSpec(targetCfg.Key)
		// Override the subpackage name.
		pkg := spec.Targets[targetCfg.Key].Packages["contrib"]
		pkg.Name = "my-custom-pkg"
		spec.Targets[targetCfg.Key].Packages["contrib"] = pkg

		// The container target installs all packages regardless of name,
		// so the binary should still be present.
		spec.Tests = []*dalec.TestSpec{
			{
				Name: "custom-named subpackage binary installed",
				Files: map[string]dalec.FileCheckOutput{
					"/usr/bin/contrib-bin": {},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
			sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(targetCfg.Container))
			solveT(ctx, t, gwc, sr)
		})
	})

	t.Run("subpackage runtime dependencies are installed", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)

		spec := subpackageSpec(targetCfg.Key)
		pkg := spec.Targets[targetCfg.Key].Packages["contrib"]
		pkg.Dependencies = &dalec.SubPackageDependencies{
			Runtime: map[string]dalec.PackageConstraints{
				"bash": {},
			},
		}
		spec.Targets[targetCfg.Key].Packages["contrib"] = pkg

		spec.Tests = []*dalec.TestSpec{
			{
				Name: "bash is available from subpackage dependency",
				Steps: []dalec.TestStep{
					{
						Command: "/bin/bash -c 'echo dep-ok'",
						Stdout:  dalec.CheckOutput{Equals: "dep-ok\n"},
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
			sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(targetCfg.Container))
			solveT(ctx, t, gwc, sr)
		})
	})

	t.Run("package target produces subpackage artifacts", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)

		spec := subpackageSpec(targetCfg.Key)

		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
			sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(targetCfg.Package))
			res := solveT(ctx, t, gwc, sr)

			// The package output should contain the subpackage file.
			// For RPM: RPMS/<arch>/test-subpkgs-contrib-*.rpm
			// For DEB: test-subpkgs-contrib_*.deb
			isRPM := strings.Contains(targetCfg.Package, "rpm")
			if isRPM {
				// Check that both the primary and subpackage RPMs exist by
				// listing the directory and verifying both names appear.
				ref, err := res.SingleRef()
				if err != nil {
					t.Fatal(err)
				}

				entries, err := ref.ReadDir(ctx, gwclient.ReadDirRequest{
					Path:           "/RPMS",
					IncludePattern: "**/*.rpm",
				})
				if err != nil {
					t.Fatal(err)
				}

				var primaryFound, contribFound bool
				for _, e := range entries {
					name := e.GetPath()
					if strings.Contains(name, "test-subpkgs-0.0.1") {
						primaryFound = true
					}
					if strings.Contains(name, "test-subpkgs-contrib-0.0.1") {
						contribFound = true
					}
				}
				if !primaryFound {
					t.Error("primary RPM not found in package output")
				}
				if !contribFound {
					t.Error("subpackage RPM (test-subpkgs-contrib) not found in package output")
				}
			} else {
				// DEB — check for both .deb files
				ref, err := res.SingleRef()
				if err != nil {
					t.Fatal(err)
				}

				entries, err := ref.ReadDir(ctx, gwclient.ReadDirRequest{
					Path:           "/",
					IncludePattern: "*.deb",
				})
				if err != nil {
					t.Fatal(err)
				}

				var primaryFound, contribFound bool
				for _, e := range entries {
					name := e.GetPath()
					if strings.HasPrefix(name, "test-subpkgs_") {
						primaryFound = true
					}
					if strings.HasPrefix(name, "test-subpkgs-contrib_") {
						contribFound = true
					}
				}
				if !primaryFound {
					t.Error("primary DEB not found in package output")
				}
				if !contribFound {
					t.Error("subpackage DEB (test-subpkgs-contrib) not found in package output")
				}
			}
		})
	})
}
