package test

import (
	"context"
	"io/fs"
	"path"
	"strings"
	"testing"

	"github.com/cavaliergopher/rpm"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend/pkg/bkfs"
	"gotest.tools/v3/assert"
)

type subpackageTestConfig struct {
	ReadPackageMetadata func(*testing.T, fs.FS, string) (subpackagePackageMetadata, bool)
	ArtifactNameMatches func(string, string) bool
}

type subpackagePackageMetadata struct {
	Name                string
	Path                string
	RuntimeDependencies map[string]struct{}
}

func rpmSubpackageTests() *subpackageTestConfig {
	return &subpackageTestConfig{
		ReadPackageMetadata: readRPMPackageMetadata,
		ArtifactNameMatches: rpmArtifactNameMatches,
	}
}

func testSubpackages(ctx context.Context, t *testing.T, targetCfg targetConfig, cfg *subpackageTestConfig) {
	t.Run("a default container installs primary and supplemental package binaries", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)

		spec := subpackageSpec(targetCfg.Key)
		spec.Tests = []*dalec.TestSpec{
			{
				Name: "primary and supplemental binaries execute",
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

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(targetCfg.Container))
			solveT(ctx, t, client, sr)
		})
	})

	t.Run("a default container installs a custom-named supplemental package", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)

		spec := subpackageSpec(targetCfg.Key)
		setSubpackageName(spec, targetCfg.Key, "my-custom-pkg")
		spec.Tests = []*dalec.TestSpec{
			{
				Name: "custom-named supplemental binary executes",
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

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(targetCfg.Container))
			solveT(ctx, t, client, sr)
		})
	})

	t.Run("a supplemental runtime dependency is written to package metadata", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)

		const dependency = "curl"
		spec := subpackageSpec(targetCfg.Key)
		pkg := spec.Targets[targetCfg.Key].Packages["contrib"]
		pkg.Dependencies = &dalec.SubPackageDependencies{
			Runtime: map[string]dalec.PackageConstraints{
				dependency: {},
			},
		}
		spec.Targets[targetCfg.Key].Packages["contrib"] = pkg

		testPackageOutput(ctx, t, targetCfg, spec, func(pkgFS fs.FS) {
			packages := readSubpackagePackageMetadata(t, pkgFS, cfg)

			pkgName := spec.Name + "-contrib"
			pkg, ok := packages[pkgName]

			assert.Assert(t, ok, "supplemental package %q was not found: %v", pkgName, packages)
			_, ok = pkg.RuntimeDependencies[dependency]
			assert.Assert(t, ok, "package %q does not require %q: %v", pkgName, dependency, pkg.RuntimeDependencies)
		})
	})

	t.Run("a package target emits primary and supplemental package artifacts", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)

		spec := subpackageSpec(targetCfg.Key)
		testPackageOutput(ctx, t, targetCfg, spec, func(pkgFS fs.FS) {
			packages := readSubpackagePackageMetadata(t, pkgFS, cfg)
			assertPackageArtifacts(t, packageArtifactAssertions{
				Config:   cfg,
				Packages: packages,
				Expected: []string{spec.Name, spec.Name + "-contrib"},
			})
		})
	})

	t.Run("a package target emits a custom supplemental name instead of the default name", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)

		spec := subpackageSpec(targetCfg.Key)
		setSubpackageName(spec, targetCfg.Key, "my-custom-pkg")

		testPackageOutput(ctx, t, targetCfg, spec, func(pkgFS fs.FS) {
			packages := readSubpackagePackageMetadata(t, pkgFS, cfg)
			assertPackageArtifacts(t, packageArtifactAssertions{
				Config:     cfg,
				Packages:   packages,
				Expected:   []string{spec.Name, "my-custom-pkg"},
				Unexpected: []string{spec.Name + "-contrib"},
			})
		})
	})
}

func subpackageSpec(targetKey string) *dalec.Spec {
	return &dalec.Spec{
		Name:        "test-subpkgs",
		Version:     "0.0.1",
		Revision:    "1",
		Description: "Test supplemental packages end-to-end",
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

func setSubpackageName(spec *dalec.Spec, targetKey, name string) {
	pkg := spec.Targets[targetKey].Packages["contrib"]
	pkg.Name = name
	spec.Targets[targetKey].Packages["contrib"] = pkg
}

func testPackageOutput(ctx context.Context, t *testing.T, targetCfg targetConfig, spec *dalec.Spec, check func(fs.FS)) {
	t.Helper()

	testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
		sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(targetCfg.Package))
		res := solveT(ctx, t, client, sr)
		ref, err := res.SingleRef()
		assert.NilError(t, err)
		check(bkfs.FromRef(ctx, ref))
	})
}

func readSubpackagePackageMetadata(t *testing.T, pkgFS fs.FS, cfg *subpackageTestConfig) map[string]subpackagePackageMetadata {
	t.Helper()

	packages := make(map[string]subpackagePackageMetadata)
	err := fs.WalkDir(pkgFS, ".", func(packagePath string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}

		metadata, ok := cfg.ReadPackageMetadata(t, pkgFS, packagePath)
		if !ok {
			return nil
		}
		metadata.Path = packagePath
		if existing, exists := packages[metadata.Name]; exists {
			t.Fatalf("package %q emitted more than once: %q and %q", metadata.Name, existing.Path, packagePath)
		}
		packages[metadata.Name] = metadata
		return nil
	})
	assert.NilError(t, err)
	assert.Assert(t, len(packages) > 0, "package target emitted no recognized package artifacts")
	return packages
}

func readRPMPackageMetadata(t *testing.T, pkgFS fs.FS, packagePath string) (subpackagePackageMetadata, bool) {
	t.Helper()

	if !strings.HasSuffix(packagePath, ".rpm") || strings.HasSuffix(packagePath, ".src.rpm") {
		return subpackagePackageMetadata{}, false
	}

	f, err := pkgFS.Open(packagePath)
	assert.NilError(t, err)
	defer f.Close()

	pkg, err := rpm.Read(f)
	assert.NilError(t, err)

	dependencies := make(map[string]struct{})
	for _, requirement := range pkg.Requires() {
		dependencies[requirement.Name()] = struct{}{}
	}

	return subpackagePackageMetadata{
		Name:                pkg.Name(),
		RuntimeDependencies: dependencies,
	}, true
}

func rpmArtifactNameMatches(packagePath, packageName string) bool {
	return strings.HasPrefix(path.Base(packagePath), packageName+"-")
}

type packageArtifactAssertions struct {
	Config     *subpackageTestConfig
	Packages   map[string]subpackagePackageMetadata
	Expected   []string
	Unexpected []string
}

func assertPackageArtifacts(t *testing.T, assertions packageArtifactAssertions) {
	t.Helper()

	for _, packageName := range assertions.Expected {
		pkg, ok := assertions.Packages[packageName]
		assert.Assert(t, ok, "package %q was not found: %v", packageName, assertions.Packages)
		assert.Assert(
			t,
			assertions.Config.ArtifactNameMatches(pkg.Path, packageName),
			"package %q has unexpected artifact name %q",
			packageName,
			pkg.Path,
		)
	}
	for _, packageName := range assertions.Unexpected {
		pkg, ok := assertions.Packages[packageName]
		assert.Assert(t, !ok, "package %q was unexpectedly emitted as %q", packageName, pkg.Path)
		for _, emitted := range assertions.Packages {
			assert.Assert(
				t,
				!assertions.Config.ArtifactNameMatches(emitted.Path, packageName),
				"package artifact %q unexpectedly uses name %q",
				emitted.Path,
				packageName,
			)
		}
	}
}
