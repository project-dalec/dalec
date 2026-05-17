package rpm

import (
	"context"
	"strings"
	"testing"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/internal/test"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

// findSpecMkfile iterates over all LLB ops and returns the path and data of the
// first Mkfile action whose path ends in ".spec".
func findSpecMkfile(t *testing.T, ops []test.LLBOp) (path string, data string) {
	t.Helper()
	for _, op := range ops {
		f := op.Op.GetFile()
		if f == nil {
			continue
		}
		for _, a := range f.Actions {
			mkfile := a.GetMkfile()
			if mkfile == nil {
				continue
			}
			if strings.HasSuffix(mkfile.Path, ".spec") {
				return mkfile.Path, string(mkfile.Data)
			}
		}
	}
	t.Fatal("no Mkfile action with .spec path found in LLB ops")
	return "", ""
}

// findMkdir iterates over all LLB ops and returns the path of the first Mkdir action.
func findMkdir(t *testing.T, ops []test.LLBOp) string {
	t.Helper()
	for _, op := range ops {
		f := op.Op.GetFile()
		if f == nil {
			continue
		}
		for _, a := range f.Actions {
			mkdir := a.GetMkdir()
			if mkdir == nil {
				continue
			}
			return mkdir.Path
		}
	}
	t.Fatal("no Mkdir action found in LLB ops")
	return ""
}

// baseSpec returns a minimal valid spec suitable for RPMSpec().
func baseSpec(name string) *dalec.Spec {
	return &dalec.Spec{
		Name:        name,
		Version:     "1.0.0",
		Revision:    "1",
		Description: "Test package",
		License:     "MIT",
	}
}

func TestRPMSpec_LLB(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("directory_and_filename", func(t *testing.T) {
		t.Parallel()
		spec := baseSpec("myapp")

		state := RPMSpec(spec, llb.Scratch(), "", "")
		ops, err := test.LLBOpsFromState(ctx, state)
		assert.NilError(t, err)

		dirPath := findMkdir(t, ops)
		assert.Equal(t, dirPath, "/SPECS/myapp")

		filePath, _ := findSpecMkfile(t, ops)
		assert.Equal(t, filePath, "/SPECS/myapp/myapp.spec")
	})

	t.Run("custom_directory", func(t *testing.T) {
		t.Parallel()
		spec := baseSpec("myapp")

		state := RPMSpec(spec, llb.Scratch(), "", "custom/dir")
		ops, err := test.LLBOpsFromState(ctx, state)
		assert.NilError(t, err)

		dirPath := findMkdir(t, ops)
		assert.Equal(t, dirPath, "/custom/dir")

		filePath, _ := findSpecMkfile(t, ops)
		assert.Equal(t, filePath, "/custom/dir/myapp.spec")
	})

	t.Run("spec_without_subpackages", func(t *testing.T) {
		t.Parallel()
		spec := baseSpec("foo")

		state := RPMSpec(spec, llb.Scratch(), "azlinux3", "")
		ops, err := test.LLBOpsFromState(ctx, state)
		assert.NilError(t, err)

		_, data := findSpecMkfile(t, ops)
		assert.Assert(t, !strings.Contains(data, "%package -n"), "spec without subpackages should not contain %%package -n")
	})

	t.Run("spec_with_subpackage_default_name", func(t *testing.T) {
		t.Parallel()
		spec := baseSpec("foo")
		spec.Targets = map[string]dalec.Target{
			"azlinux3": {
				Packages: map[string]dalec.SubPackage{
					"debug": {
						Description: "Debug symbols for foo",
						Artifacts: &dalec.Artifacts{
							Binaries: map[string]dalec.ArtifactConfig{
								"foo-debug": {},
							},
						},
					},
				},
			},
		}

		state := RPMSpec(spec, llb.Scratch(), "azlinux3", "")
		ops, err := test.LLBOpsFromState(ctx, state)
		assert.NilError(t, err)

		_, data := findSpecMkfile(t, ops)

		assert.Assert(t, cmp.Contains(data, "%package -n foo-debug"))
		assert.Assert(t, cmp.Contains(data, "%description -n foo-debug"))
		assert.Assert(t, cmp.Contains(data, "Debug symbols for foo"))
		assert.Assert(t, cmp.Contains(data, "%files -n foo-debug"))
		assert.Assert(t, cmp.Contains(data, "%{_bindir}/foo-debug"))
	})

	t.Run("spec_with_subpackage_custom_name", func(t *testing.T) {
		t.Parallel()
		spec := baseSpec("foo")
		spec.Targets = map[string]dalec.Target{
			"azlinux3": {
				Packages: map[string]dalec.SubPackage{
					"compat": {
						Name:        "foo-compat-v2",
						Description: "Backward compatibility shim",
						Artifacts: &dalec.Artifacts{
							Binaries: map[string]dalec.ArtifactConfig{
								"foo-v2": {},
							},
						},
					},
				},
			},
		}

		state := RPMSpec(spec, llb.Scratch(), "azlinux3", "")
		ops, err := test.LLBOpsFromState(ctx, state)
		assert.NilError(t, err)

		_, data := findSpecMkfile(t, ops)

		assert.Assert(t, cmp.Contains(data, "%package -n foo-compat-v2"))
		assert.Assert(t, cmp.Contains(data, "%description -n foo-compat-v2"))
		assert.Assert(t, cmp.Contains(data, "%files -n foo-compat-v2"))
		assert.Assert(t, cmp.Contains(data, "%{_bindir}/foo-v2"))
		// Should NOT contain the default derived name
		assert.Assert(t, !strings.Contains(data, "%package -n foo-compat\n"), "should use custom name, not default")
	})

	t.Run("spec_with_subpackage_dependencies", func(t *testing.T) {
		t.Parallel()
		spec := baseSpec("foo")
		spec.Targets = map[string]dalec.Target{
			"azlinux3": {
				Packages: map[string]dalec.SubPackage{
					"devel": {
						Description: "Development files for foo",
						Dependencies: &dalec.SubPackageDependencies{
							Runtime: dalec.PackageDependencyList{
								"foo": dalec.PackageConstraints{
									Version: []string{"= %{version}-%{release}"},
								},
								"libfoo-headers": {},
							},
							Recommends: dalec.PackageDependencyList{
								"foo-docs": {},
							},
						},
						Provides: dalec.PackageDependencyList{
							"foo-dev": {},
						},
						Conflicts: dalec.PackageDependencyList{
							"foo-devel-old": {
								Version: []string{"< 1.0"},
							},
						},
						Replaces: dalec.PackageDependencyList{
							"foo-devel-legacy": {},
						},
					},
				},
			},
		}

		state := RPMSpec(spec, llb.Scratch(), "azlinux3", "")
		ops, err := test.LLBOpsFromState(ctx, state)
		assert.NilError(t, err)

		_, data := findSpecMkfile(t, ops)

		assert.Assert(t, cmp.Contains(data, "%package -n foo-devel"))
		assert.Assert(t, cmp.Contains(data, "Requires: foo == %{version}-%{release}"))
		assert.Assert(t, cmp.Contains(data, "Requires: libfoo-headers"))
		assert.Assert(t, cmp.Contains(data, "Recommends: foo-docs"))
		assert.Assert(t, cmp.Contains(data, "Provides: foo-dev"))
		assert.Assert(t, cmp.Contains(data, "Conflicts: foo-devel-old < 1.0"))
		assert.Assert(t, cmp.Contains(data, "Obsoletes: foo-devel-legacy"))
	})

	t.Run("spec_with_multiple_subpackages_sorted", func(t *testing.T) {
		t.Parallel()
		spec := baseSpec("foo")
		spec.Targets = map[string]dalec.Target{
			"azlinux3": {
				Packages: map[string]dalec.SubPackage{
					"debug": {
						Description: "Debug package",
						Artifacts: &dalec.Artifacts{
							Binaries: map[string]dalec.ArtifactConfig{
								"foo-debug": {},
							},
						},
					},
					"contrib": {
						Description: "Contrib package",
						Artifacts: &dalec.Artifacts{
							Binaries: map[string]dalec.ArtifactConfig{
								"foo-contrib": {},
							},
						},
					},
				},
			},
		}

		state := RPMSpec(spec, llb.Scratch(), "azlinux3", "")
		ops, err := test.LLBOpsFromState(ctx, state)
		assert.NilError(t, err)

		_, data := findSpecMkfile(t, ops)

		assert.Assert(t, cmp.Contains(data, "%package -n foo-contrib"))
		assert.Assert(t, cmp.Contains(data, "%package -n foo-debug"))

		// contrib should come before debug (sorted by key)
		contribIdx := strings.Index(data, "%package -n foo-contrib")
		debugIdx := strings.Index(data, "%package -n foo-debug")
		assert.Assert(t, contribIdx < debugIdx, "contrib should appear before debug (sorted by key)")
	})

	t.Run("spec_with_subpackage_install_artifacts", func(t *testing.T) {
		t.Parallel()
		spec := baseSpec("foo")
		spec.Artifacts = dalec.Artifacts{
			Binaries: map[string]dalec.ArtifactConfig{
				"foo": {},
			},
		}
		spec.Targets = map[string]dalec.Target{
			"azlinux3": {
				Packages: map[string]dalec.SubPackage{
					"debug": {
						Description: "Debug symbols",
						Artifacts: &dalec.Artifacts{
							Binaries: map[string]dalec.ArtifactConfig{
								"foo-debug": {},
							},
						},
					},
				},
			},
		}

		state := RPMSpec(spec, llb.Scratch(), "azlinux3", "")
		ops, err := test.LLBOpsFromState(ctx, state)
		assert.NilError(t, err)

		_, data := findSpecMkfile(t, ops)

		// %install section should include both primary and subpackage artifacts
		assert.Assert(t, cmp.Contains(data, "cp -r foo %{buildroot}/%{_bindir}/foo"))
		assert.Assert(t, cmp.Contains(data, "cp -r foo-debug %{buildroot}/%{_bindir}/foo-debug"))
	})

	t.Run("wrong_target_no_subpackages", func(t *testing.T) {
		t.Parallel()
		spec := baseSpec("foo")
		spec.Targets = map[string]dalec.Target{
			"azlinux3": {
				Packages: map[string]dalec.SubPackage{
					"debug": {
						Description: "Debug",
					},
				},
			},
		}

		state := RPMSpec(spec, llb.Scratch(), "jammy", "")
		ops, err := test.LLBOpsFromState(ctx, state)
		assert.NilError(t, err)

		_, data := findSpecMkfile(t, ops)
		assert.Assert(t, !strings.Contains(data, "%package -n"), "wrong target should not produce subpackage sections")
	})

	t.Run("invalid_spec_returns_error", func(t *testing.T) {
		t.Parallel()
		// Spec missing required fields (Name, Version, etc.)
		spec := &dalec.Spec{}

		state := RPMSpec(spec, llb.Scratch(), "", "")
		_, err := test.LLBOpsFromState(ctx, state)
		assert.Assert(t, err != nil, "invalid spec should produce an error state that fails to marshal")
	})
}
