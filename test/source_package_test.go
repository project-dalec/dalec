package test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"testing"

	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend/pkg/bkfs"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

// testSourceOutputBuilds is a regression test for a nil image-config panic in
// the "output-only" targets that emit the package source form (deb "/dsc",
// rpm "/rpm/debug/sources"). The deb handler returned a nil image config, which
// buildkit dereferenced when assembling the export platform. Building the
// target is enough to exercise that path across distros.
func testSourceOutputBuilds(ctx context.Context, t *testing.T, targetCfg targetConfig) {
	var sourceTarget string
	switch {
	case strings.HasSuffix(targetCfg.Package, "/deb"):
		sourceTarget = strings.TrimSuffix(targetCfg.Package, "/deb") + "/dsc"
	case strings.HasSuffix(targetCfg.Package, "/rpm"):
		sourceTarget = targetCfg.Package + "/debug/sources"
	default:
		t.Skipf("no source-output target known for package target %q", targetCfg.Package)
	}

	spec := &dalec.Spec{
		Name:        "test-dalec-source-output",
		Version:     "0.0.1",
		Revision:    "1",
		Description: "Testing source output target builds",
		License:     "MIT",
		Sources: map[string]dalec.Source{
			"src": {
				Inline: &dalec.SourceInline{
					File: &dalec.SourceInlineFile{Contents: "hello world"},
				},
			},
		},
		Artifacts: dalec.Artifacts{
			Binaries: map[string]dalec.ArtifactConfig{
				"src": {},
			},
		},
		Build: dalec.ArtifactBuild{
			Steps: []dalec.BuildStep{
				{Command: "true"},
			},
		},
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
		sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(sourceTarget))
		solveT(ctx, t, gwc, sr)
	})
}

// testSourceOutputAppliesGomodEdits verifies that spec preprocessing (gomod
// replace directives) is applied when producing the deb source package output.
// The handler previously skipped Preprocess, silently emitting a source package
// missing the generated gomod patch.
func testSourceOutputAppliesGomodEdits(ctx context.Context, t *testing.T, targetCfg targetConfig) {
	if !strings.HasSuffix(targetCfg.Package, "/deb") {
		t.Skip("source-package preprocessing check only implemented for deb /dsc")
	}
	sourceTarget := strings.TrimSuffix(targetCfg.Package, "/deb") + "/dsc"

	spec := &dalec.Spec{
		Name:        "test-dalec-dsc-preprocess",
		Version:     "0.0.1",
		Revision:    "1",
		License:     "MIT",
		Description: "gomod edits must be preprocessed into the source package",
		Sources: map[string]dalec.Source{
			"src": {
				Generate: []*dalec.SourceGenerator{
					{
						Gomod: &dalec.GeneratorGomod{
							Edits: &dalec.GomodEdits{
								Replace: []dalec.GomodReplace{
									{Original: "github.com/cpuguy83/tar2go@v0.3.1", Update: "github.com/cpuguy83/tar2go@v0.3.0"},
								},
							},
						},
					},
				},
				Inline: &dalec.SourceInline{
					Dir: &dalec.SourceInlineDir{
						Files: map[string]*dalec.SourceInlineFile{
							"main.go": {Contents: gomodFixtureMain},
							// go 1.18 so `go mod tidy` (run by Preprocess) works on
							// distros shipping older Go toolchains (e.g. Jammy's 1.18).
							"go.mod": {Contents: "module testgomodsource\n\ngo 1.18\n\nrequire github.com/cpuguy83/tar2go v0.3.1\n"},
							"go.sum": {Contents: gomodFixtureSum},
						},
					},
				},
			},
		},
		Dependencies: &dalec.PackageDependencies{
			Build: map[string]dalec.PackageConstraints{
				targetCfg.GetPackage("golang"): {},
			},
		},
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
		res := solveT(ctx, t, gwc, newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(sourceTarget)))

		// Pull dalec-changes.patch out of the source package and confirm the
		// gomod replace directive made it in (i.e. Preprocess ran).
		st := llb.Image("alpine:latest").
			Run(dalec.ShArgs("apk add --no-cache tar xz")).Root().
			Run(
				dalec.ShArgs(`set -e; mkdir -p /out /x; for f in /src/*.debian.tar.xz; do tar -xJf "$f" -C /x; done; cp /x/debian/patches/dalec-changes.patch /out/patch`),
				llb.AddMount("/src", resultToState(t, res), llb.Readonly),
			).AddMount("/out", llb.Scratch())

		def, err := st.Marshal(ctx)
		assert.NilError(t, err)
		out, err := gwc.Solve(ctx, gwclient.SolveRequest{Definition: def.ToPB(), Evaluate: true})
		assert.NilError(t, err)

		patch := string(readFile(ctx, t, "patch", out))
		assert.Check(t, strings.Contains(patch, "replace github.com/cpuguy83/tar2go"),
			"source package patch must contain the gomod replace directive (Preprocess must run for source packages), got:\n%s", patch)
	})
}

// testSourceRPMTarget verifies the `<distro>/srpm` target produces the same
// source rpm a full `<distro>/rpm` build produces, without running the spec's
// build steps (i.e. rpmbuild's %build/binary rpm stages never run).
func testSourceRPMTarget(ctx context.Context, t *testing.T, targetCfg targetConfig) {
	if !strings.HasSuffix(targetCfg.Package, "/rpm") {
		t.Skipf("srpm target is not available for package target %q", targetCfg.Package)
	}
	srpmTarget := strings.TrimSuffix(targetCfg.Package, "/rpm") + "/srpm"

	spec := &dalec.Spec{
		Name:        "test-dalec-srpm-target",
		Version:     "0.0.1",
		Revision:    "1",
		Description: "Testing the source rpm only target",
		License:     "MIT",
		Sources: map[string]dalec.Source{
			"src": {
				Inline: &dalec.SourceInline{
					File: &dalec.SourceInlineFile{Contents: "hello world"},
				},
			},
		},
		Artifacts: dalec.Artifacts{
			Binaries: map[string]dalec.ArtifactConfig{
				"src": {},
			},
		},
		Build: dalec.ArtifactBuild{
			Steps: []dalec.BuildStep{
				// The srpm target must not execute build steps. If %build runs
				// this fails the build and therefore the test.
				{Command: "exit 42"},
			},
		},
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
		sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(srpmTarget))
		res := solveT(ctx, t, gwc, sr)

		ref, err := res.SingleRef()
		assert.NilError(t, err)

		// The source rpm must land in the same place a full rpm build puts it.
		srpmPath := expectedSRPMPath(t, targetCfg, spec)
		_, err = ref.StatFile(ctx, gwclient.StatRequest{Path: srpmPath})
		assert.NilError(t, err, "expected source rpm at %q", srpmPath)

		// ... and nothing else: with `-bs` rpmbuild never populates the binary
		// rpm output dir, so `SRPMS` must be the only thing in the output.
		ents, err := ref.ReadDir(ctx, gwclient.ReadDirRequest{Path: "/"})
		assert.NilError(t, err)

		names := make([]string, 0, len(ents))
		for _, e := range ents {
			names = append(names, e.Path)
		}
		assert.Check(t, cmp.DeepEqual(names, []string{"SRPMS"}), "srpm target output should only contain SRPMS, got %v", names)
	})
}

// testSourcePackageExcludesGomodZipCache checks the actual source package, not
// just the generator output: cache ZIPs must be omitted without losing ordinary
// sources (including ZIPs), extracted modules, or module metadata.
func testSourcePackageExcludesGomodZipCache(ctx context.Context, t *testing.T, targetCfg targetConfig) {
	const (
		contextName = "gomod-source"
		module      = "github.com/cpuguy83/tar2go"
		version     = "v0.3.1"
		moduleDir   = module + "@" + version
		cacheDir    = "cache/download/" + module + "/@v/"
	)

	var sourceTarget string
	switch {
	case strings.HasSuffix(targetCfg.Package, "/rpm"):
		sourceTarget = strings.TrimSuffix(targetCfg.Package, "/rpm") + "/srpm"
	case strings.HasSuffix(targetCfg.Package, "/deb"):
		sourceTarget = strings.TrimSuffix(targetCfg.Package, "/deb") + "/dsc"
	default:
		t.Fatalf("no source-package target known for %q", targetCfg.Package)
	}

	zipFixture := gomodZipFixture(t)
	source := llb.Scratch().
		File(llb.Mkfile("/main.go", 0o644, []byte(gomodFixtureMain))).
		File(llb.Mkfile("/go.mod", 0o644, []byte(gomodFixtureMod))).
		File(llb.Mkfile("/go.sum", 0o644, []byte(gomodFixtureSum))).
		File(llb.Mkdir("/testdata", 0o755)).
		File(llb.Mkfile("/testdata/fixture.zip", 0o644, zipFixture))
	spec := &dalec.Spec{
		Name:        "test-gomod-source",
		Version:     "0.0.1",
		Revision:    "1",
		Description: "Testing module cache ZIP exclusion in source packages",
		License:     "MIT",
		Website:     "https://github.com/project-dalec/dalec",
		Vendor:      "Dalec",
		Packager:    "Dalec",
		Sources: map[string]dalec.Source{
			"src": {
				Context:  &dalec.SourceContext{Name: contextName},
				Generate: []*dalec.SourceGenerator{{Gomod: &dalec.GeneratorGomod{}}},
			},
		},
		Dependencies: &dalec.PackageDependencies{
			Build: map[string]dalec.PackageConstraints{
				targetCfg.GetPackage("golang"): {},
			},
		},
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
		res := solveT(ctx, t, gwc, newSolveRequest(
			withSpec(ctx, t, spec),
			withBuildContext(ctx, t, contextName, source),
			withBuildTarget(sourceTarget),
		))
		ref, err := res.SingleRef()
		assert.NilError(t, err)
		pkgFS := bkfs.FromRef(ctx, ref)

		var extract, gomodsRoot, sourceRoot string
		if strings.HasSuffix(targetCfg.Package, "/rpm") {
			srpm := expectedSRPMPath(t, targetCfg, spec)
			_, err := fs.Stat(pkgFS, srpm)
			assert.NilError(t, err, "expected source RPM at %q", srpm)
			// Keep rpm2cpio separate from cpio so either failure is surfaced.
			extract = fmt.Sprintf(`set -eu
mkdir -p /work /out/gomods
cd /work
rpm2cpio %q > source.cpio
cpio -id < source.cpio
tar -xzf __gomods.tar.gz -C /out/gomods
tar -xzf src.tar.gz -C /out
`, path.Join("/pkg", srpm))
			gomodsRoot, sourceRoot = "gomods", "src"
		} else {
			descriptors, err := fs.Glob(pkgFS, "*.dsc")
			assert.NilError(t, err)
			assert.Assert(t, cmp.Len(descriptors, 1), "expected exactly one source descriptor")
			for _, name := range []string{descriptors[0], spec.Name + "_" + spec.Version + ".orig.tar.gz"} {
				_, err := fs.Stat(pkgFS, name)
				assert.NilError(t, err, "expected source-package artifact %q", name)
			}
			extract = fmt.Sprintf("set -eu; dpkg-source -x %q /out/source", path.Join("/pkg", descriptors[0]))
			gomodsRoot, sourceRoot = "source/xxxdalecGomodsInternal", "source/src"
		}

		worker := solveT(ctx, t, gwc, newSolveRequest(withBuildTarget(targetCfg.Worker), withSpec(ctx, t, nil)))
		st := resultToState(t, worker).Run(
			dalec.ShArgs(extract),
			llb.AddMount("/pkg", resultToState(t, res), llb.Readonly),
		).AddMount("/out", llb.Scratch())

		def, err := st.Marshal(ctx)
		assert.NilError(t, err)
		out, err := gwc.Solve(ctx, gwclient.SolveRequest{Definition: def.ToPB(), Evaluate: true})
		assert.NilError(t, err)
		outRef, err := out.SingleRef()
		assert.NilError(t, err)
		gomods, err := fs.Sub(bkfs.FromRef(ctx, outRef), gomodsRoot)
		assert.NilError(t, err)

		stat, err := fs.Stat(gomods, moduleDir)
		assert.NilError(t, err, "extracted module must remain in the source package")
		assert.Assert(t, stat.IsDir())
		mod, err := fs.ReadFile(gomods, moduleDir+"/go.mod")
		assert.NilError(t, err)
		assert.Assert(t, len(mod) > 0, "module go.mod must not be empty")
		checkFile(ctx, t, path.Join(gomodsRoot, cacheDir, version+".mod"), out, mod)

		var hasGoSource bool
		err = fs.WalkDir(gomods, moduleDir, func(name string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() && strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
				data, err := fs.ReadFile(gomods, name)
				if err != nil {
					return err
				}
				hasGoSource = hasGoSource || len(data) > 0
			}
			return nil
		})
		assert.NilError(t, err)
		assert.Assert(t, hasGoSource, "module must retain nonempty non-test Go sources")

		var info struct {
			Version string
		}
		assert.NilError(t, json.Unmarshal(readFile(ctx, t, path.Join(gomodsRoot, cacheDir, version+".info"), out), &info))
		assert.Equal(t, info.Version, version)
		var checksum string
		for line := range strings.SplitSeq(gomodFixtureSum, "\n") {
			fields := strings.Fields(line)
			if len(fields) == 3 && fields[0] == module && fields[1] == version {
				checksum = fields[2]
				break
			}
		}
		assert.Assert(t, checksum != "", "fixture must contain the module ZIP checksum")
		assert.Equal(t, strings.TrimSpace(string(readFile(ctx, t, path.Join(gomodsRoot, cacheDir, version+".ziphash"), out))), checksum)

		zipPath := cacheDir + version + ".zip"
		_, err = fs.Stat(gomods, zipPath)
		assert.Assert(t, errors.Is(err, fs.ErrNotExist), "expected cache ZIP %q to be excluded, got %v", zipPath, err)
		err = fs.WalkDir(gomods, "cache/download", func(name string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if strings.HasSuffix(name, ".zip") {
				return fmt.Errorf("unexpected ZIP in packaged module download cache: %s", name)
			}
			return nil
		})
		assert.NilError(t, err)

		checkFile(ctx, t, sourceRoot+"/main.go", out, []byte(gomodFixtureMain))
		checkFile(ctx, t, sourceRoot+"/go.mod", out, []byte(gomodFixtureMod))
		checkFile(ctx, t, sourceRoot+"/go.sum", out, []byte(gomodFixtureSum))
		checkFile(ctx, t, sourceRoot+"/testdata/fixture.zip", out, zipFixture)
	})
}

// expectedSRPMPath returns the path of the source rpm the distro's rpm target
// produces for the given spec.
func expectedSRPMPath(t *testing.T, targetCfg targetConfig, spec *dalec.Spec) string {
	t.Helper()

	if targetCfg.ListExpectedSignFiles == nil {
		t.Fatal("target config is missing ListExpectedSignFiles")
	}

	for _, f := range targetCfg.ListExpectedSignFiles(spec, platforms.DefaultSpec()) {
		if strings.HasSuffix(f, ".src.rpm") {
			return f
		}
	}

	t.Fatal("no source rpm in the list of expected package files")
	return ""
}

func gomodZipFixture(t *testing.T) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	entry, err := zw.Create("fixture.txt")
	assert.NilError(t, err)
	_, err = entry.Write([]byte("retained ZIP contents\n"))
	assert.NilError(t, err)
	assert.NilError(t, zw.Close())
	return buf.Bytes()
}
