package rpm

import (
	"strings"
	"testing"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/internal/test"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestRPMSSpec_Buildroot_respects_directory_arg(t *testing.T) {
	t.Parallel()

	t.Run("when the directory specified is left empty", func(t *testing.T) {
		t.Parallel()
		spec := baseSpec("myapp")

		state := RPMSpec(spec, llb.Scratch(), "", "")

		t.Run("the build root is put into /", func(t *testing.T) {
			t.Parallel()
			ops := test.LLBOpsFromState(t.Context(), t, state)

			dirPath := findMkdir(t, ops)
			assert.Equal(t, dirPath, "/SPECS/myapp")

			filePath, _ := findSpecMkfile(t, ops)
			assert.Equal(t, filePath, "/SPECS/myapp/myapp.spec")
		})
	})

	t.Run("when the directory specified is non-empty", func(t *testing.T) {
		t.Parallel()
		spec := baseSpec("myapp")
		state := RPMSpec(spec, llb.Scratch(), "", "custom/dir")

		t.Run("the build root is put into that directory", func(t *testing.T) {
			t.Parallel()
			ops := test.LLBOpsFromState(t.Context(), t, state)

			dirPath := findMkdir(t, ops)
			assert.Equal(t, dirPath, "/custom/dir")

			filePath, _ := findSpecMkfile(t, ops)
			assert.Equal(t, filePath, "/custom/dir/myapp.spec")
		})
	})
}

func TestRPMSpec_Subpackages(t *testing.T) {
	t.Parallel()

	t.Run("when a spec has no subpackages", func(t *testing.T) {
		t.Parallel()
		spec := baseSpec("foo")

		state := RPMSpec(spec, llb.Scratch(), "azlinux3", "")
		ops := test.LLBOpsFromState(t.Context(), t, state)

		t.Run("then the rpm spec does not have subpackage markers", func(t *testing.T) {
			t.Parallel()
			_, data := findSpecMkfile(t, ops)
			assert.Assert(t, !strings.Contains(data, "%package -n"))
			assert.Assert(t, !strings.Contains(data, "%description -n"))
			assert.Assert(t, !strings.Contains(data, "%files -n"))
		})
	})

	t.Run("given a spec has subpackages", func(t *testing.T) {
		t.Parallel()

		t.Run("when the subpackage uses the default naming", func(t *testing.T) {
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

			t.Run("the rpm spec has subpackage markers matching the default name", func(t *testing.T) {
				t.Parallel()
				state := RPMSpec(spec, llb.Scratch(), "azlinux3", "")
				ops := test.LLBOpsFromState(t.Context(), t, state)

				_, data := findSpecMkfile(t, ops)

				assert.Assert(t, cmp.Contains(data, "%package -n foo-debug"))
				assert.Assert(t, cmp.Contains(data, "%description -n foo-debug"))
				assert.Assert(t, cmp.Contains(data, "Debug symbols for foo"))
				assert.Assert(t, cmp.Contains(data, "%files -n foo-debug"))
				assert.Assert(t, cmp.Contains(data, "%{_bindir}/foo-debug"))
			})
		})

		t.Run("when the subpackage has a custom name", func(t *testing.T) {
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

			t.Run("the rpm spec has subpackage markers matching the custom name", func(t *testing.T) {
				t.Parallel()

				state := RPMSpec(spec, llb.Scratch(), "azlinux3", "")
				ops := test.LLBOpsFromState(t.Context(), t, state)

				_, data := findSpecMkfile(t, ops)

				assert.Assert(t, cmp.Contains(data, "%package -n foo-compat-v2"))
				assert.Assert(t, cmp.Contains(data, "%description -n foo-compat-v2"))
				assert.Assert(t, cmp.Contains(data, "%files -n foo-compat-v2"))
				assert.Assert(t, cmp.Contains(data, "%{_bindir}/foo-v2"))
				// Should NOT contain the default derived name
				assert.Assert(t, !strings.Contains(data, "%package -n foo-compat\n"), "should use custom name, not default")
			})
		})

		t.Run("when a subpackage declares package relationships", func(t *testing.T) {
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

			t.Run("the rpm spec includes the declared package relationships", func(t *testing.T) {
				t.Parallel()
				state := RPMSpec(spec, llb.Scratch(), "azlinux3", "")
				ops := test.LLBOpsFromState(t.Context(), t, state)

				_, data := findSpecMkfile(t, ops)

				assert.Assert(t, cmp.Contains(data, "%package -n foo-devel"))
				assert.Assert(t, cmp.Contains(data, "Requires: foo == %{version}-%{release}"))
				assert.Assert(t, cmp.Contains(data, "Requires: libfoo-headers"))
				assert.Assert(t, cmp.Contains(data, "Recommends: foo-docs"))
				assert.Assert(t, cmp.Contains(data, "Provides: foo-dev"))
				assert.Assert(t, cmp.Contains(data, "Conflicts: foo-devel-old < 1.0"))
				assert.Assert(t, cmp.Contains(data, "Obsoletes: foo-devel-legacy"))
			})
		})

		t.Run("when the spec has multiple subpackages", func(t *testing.T) {
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

			t.Run("the rpm spec orders the subpackage sections by key", func(t *testing.T) {
				t.Parallel()
				state := RPMSpec(spec, llb.Scratch(), "azlinux3", "")
				ops := test.LLBOpsFromState(t.Context(), t, state)

				_, data := findSpecMkfile(t, ops)

				assert.Assert(t, cmp.Contains(data, "%package -n foo-contrib"))
				assert.Assert(t, cmp.Contains(data, "%package -n foo-debug"))

				contribIdx := strings.Index(data, "%package -n foo-contrib")
				debugIdx := strings.Index(data, "%package -n foo-debug")
				assert.Assert(t, contribIdx < debugIdx)
			})
		})

		t.Run("when the root package and a subpackage have artifacts", func(t *testing.T) {
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

			t.Run("the install section includes artifacts from both packages", func(t *testing.T) {
				t.Parallel()
				state := RPMSpec(spec, llb.Scratch(), "azlinux3", "")
				ops := test.LLBOpsFromState(t.Context(), t, state)

				_, data := findSpecMkfile(t, ops)

				assert.Assert(t, cmp.Contains(data, "cp -r foo %{buildroot}/%{_bindir}/foo"))
				assert.Assert(t, cmp.Contains(data, "cp -r foo-debug %{buildroot}/%{_bindir}/foo-debug"))
			})
		})

		t.Run("when a target without subpackages is selected", func(t *testing.T) {
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

			t.Run("the rpm spec does not have subpackage markers", func(t *testing.T) {
				t.Parallel()
				state := RPMSpec(spec, llb.Scratch(), "jammy", "")
				ops := test.LLBOpsFromState(t.Context(), t, state)

				_, data := findSpecMkfile(t, ops)
				assert.Assert(t, !strings.Contains(data, "%package -n"))
			})
		})
	})
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
