package dalec

import (
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestSubPackageResolvedName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		pkg        SubPackage
		parentName string
		mapKey     string
		expected   string
	}{
		{
			name:       "default naming",
			pkg:        SubPackage{Description: "test"},
			parentName: "foo",
			mapKey:     "debug",
			expected:   "foo-debug",
		},
		{
			name:       "explicit name override",
			pkg:        SubPackage{Name: "custom-pkg", Description: "test"},
			parentName: "foo",
			mapKey:     "debug",
			expected:   "custom-pkg",
		},
		{
			name:       "empty explicit name uses default",
			pkg:        SubPackage{Name: "", Description: "test"},
			parentName: "bar",
			mapKey:     "contrib",
			expected:   "bar-contrib",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.pkg.ResolvedName(tt.parentName, tt.mapKey)
			assert.Check(t, cmp.Equal(got, tt.expected))
		})
	}
}

func TestRootPackageArtifactControls(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		artifacts Artifacts
	}{
		{
			name: "A root package with disable_strip enabled is valid",
			artifacts: Artifacts{
				DisableStrip: true,
			},
		},
		{
			name: "A root package with disable_auto_requires enabled is valid",
			artifacts: Artifacts{
				DisableAutoRequires: true,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			spec := Spec{
				Name:        "tools",
				Description: "Tools package",
				Website:     "https://example.com",
				Version:     "1.0.0",
				Revision:    "1",
				License:     "MIT",
				Artifacts:   tc.artifacts,
			}

			err := spec.Validate()

			assert.NilError(t, err)
		})
	}
}

func TestSubPackageBuildArgumentSubstitution(t *testing.T) {
	t.Parallel()

	t.Run("A runtime dependency version containing a build argument is substituted", func(t *testing.T) {
		t.Parallel()

		spec := Spec{
			Args: map[string]string{
				"PACKAGE_VERSION": "",
			},
			Targets: map[string]Target{
				"linux": {
					Packages: map[string]SubPackage{
						"tools": {
							Name:        "tools",
							Description: "Tools package",
							Dependencies: &SubPackageDependencies{
								Runtime: PackageDependencyList{
									"runtime": {
										Version: []string{"=${PACKAGE_VERSION}"},
									},
								},
							},
						},
					},
				},
			},
		}

		err := spec.SubstituteArgs(map[string]string{"PACKAGE_VERSION": "1.2.3"})

		assert.NilError(t, err)
		pkg := spec.Targets["linux"].Packages["tools"]
		assert.DeepEqual(t, pkg.Dependencies.Runtime["runtime"].Version, []string{"=1.2.3"})
	})
}

func TestSubPackageFieldsSurviveYAMLMarshalAndUnmarshal(t *testing.T) {
	t.Parallel()

	original := SubPackage{
		Name:        "custom-name",
		Description: "A custom subpackage",
		Artifacts: &Artifacts{
			Binaries: map[string]ArtifactConfig{
				"foo-contrib": {SubPath: "foo"},
			},
		},
		Dependencies: &SubPackageDependencies{
			Runtime: PackageDependencyList{
				"openssl-libs": {},
			},
			Recommends: PackageDependencyList{
				"suggested-pkg": {},
			},
		},
		Conflicts: PackageDependencyList{
			"foo": {},
		},
		Provides: PackageDependencyList{
			"foo": {},
		},
		Replaces: PackageDependencyList{
			"old-foo": {},
		},
	}

	data, err := yaml.Marshal(original)
	assert.NilError(t, err)

	var roundTripped SubPackage
	err = yaml.Unmarshal(data, &roundTripped)
	assert.NilError(t, err)

	assert.Check(t, cmp.Equal(roundTripped.Name, original.Name))
	assert.Check(t, cmp.Equal(roundTripped.Description, original.Description))

	// Check artifacts
	assert.Check(t, roundTripped.Artifacts != nil)
	assert.Check(t, cmp.Equal(len(roundTripped.Artifacts.Binaries), 1))

	// Check dependencies
	assert.Check(t, roundTripped.Dependencies != nil)
	assert.Check(t, cmp.Equal(len(roundTripped.Dependencies.GetRuntime()), 1))
	assert.Check(t, cmp.Equal(len(roundTripped.Dependencies.GetRecommends()), 1))

	// Check conflicts/provides/replaces
	assert.Check(t, cmp.Equal(len(roundTripped.Conflicts), 1))
	assert.Check(t, cmp.Equal(len(roundTripped.Provides), 1))
	assert.Check(t, cmp.Equal(len(roundTripped.Replaces), 1))
}

func TestTargetWithSubPackagesYAML(t *testing.T) {
	t.Parallel()

	input := `
targets:
  azlinux3:
    packages:
      debug:
        description: "Debug symbols for foo"
        artifacts:
          binaries:
            dbg/foo:
              name: foo.dbg
        dependencies:
          runtime:
            foo: {}
      contrib:
        name: foo-contrib-custom
        description: "Foo with contrib extensions"
        conflicts:
          foo: {}
        provides:
          foo: {}
`

	var spec Spec
	err := yaml.Unmarshal([]byte(input), &spec)
	assert.NilError(t, err)

	target, ok := spec.Targets["azlinux3"]
	assert.Check(t, ok, "expected azlinux3 target")
	assert.Check(t, cmp.Equal(len(target.Packages), 2))

	debugPkg, ok := target.Packages["debug"]
	assert.Check(t, ok, "expected debug package")
	assert.Check(t, cmp.Equal(debugPkg.Description, "Debug symbols for foo"))
	assert.Check(t, cmp.Equal(debugPkg.ResolvedName("foo", "debug"), "foo-debug"))

	contribPkg, ok := target.Packages["contrib"]
	assert.Check(t, ok, "expected contrib package")
	assert.Check(t, cmp.Equal(contribPkg.Name, "foo-contrib-custom"))
	assert.Check(t, cmp.Equal(contribPkg.ResolvedName("foo", "contrib"), "foo-contrib-custom"))
}

func TestGetSubPackagesForTarget(t *testing.T) {
	t.Parallel()

	spec := &Spec{
		Name: "foo",
		Targets: map[string]Target{
			"azlinux3": {
				Packages: map[string]SubPackage{
					"debug": {
						Description: "debug",
					},
					"contrib": {
						Name:        "custom-contrib",
						Description: "contrib",
					},
				},
			},
			"jammy": {},
			"other": {
				Packages: map[string]SubPackage{
					"other": {
						Description: "other",
					},
				},
			},
		},
	}

	t.Run("A target with subpackages yields only its packages in map-key order", func(t *testing.T) {
		t.Parallel()

		var keys []string
		var packages []SubPackage
		for key, pkg := range GetSubPackagesForTarget(spec, "azlinux3") {
			keys = append(keys, key)
			packages = append(packages, pkg)
		}

		assert.DeepEqual(t, keys, []string{"contrib", "debug"})
		assert.Equal(t, packages[0].Name, "custom-contrib")
		assert.Equal(t, packages[1].Description, "debug")
	})

	t.Run("A target without subpackages yields no values", func(t *testing.T) {
		t.Parallel()

		var count int
		for range GetSubPackagesForTarget(spec, "jammy") {
			count++
		}

		assert.Equal(t, count, 0)
	})

	t.Run("A missing target yields no values", func(t *testing.T) {
		t.Parallel()

		var count int
		for range GetSubPackagesForTarget(spec, "nonexistent") {
			count++
		}

		assert.Equal(t, count, 0)
	})
}

func TestSubPackageDependenciesNilSafe(t *testing.T) {
	t.Parallel()

	t.Run("nil dependency accessors return nil", func(t *testing.T) {
		t.Parallel()

		var d *SubPackageDependencies
		assert.Check(t, d.GetRuntime() == nil)
		assert.Check(t, d.GetRecommends() == nil)
	})

	t.Run("a subpackage with no dependencies substitutes args without error", func(t *testing.T) {
		t.Parallel()

		spec := Spec{
			Targets: map[string]Target{
				"linux": {
					Packages: map[string]SubPackage{
						"tools": {Description: "Tools package"},
					},
				},
			},
		}

		assert.NilError(t, spec.SubstituteArgs(nil))
	})
}

func TestSpecValidateWithSubPackages(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		packages  map[string]SubPackage
		wantError string
	}{
		{
			name: "Supplemental packages with artifacts, dependencies, and custom names are accepted",
			packages: map[string]SubPackage{
				"debug": {
					Description: "Debug symbols",
					Artifacts: &Artifacts{
						Binaries: map[string]ArtifactConfig{
							"dbg/foo": {SubPath: "foo.dbg"},
						},
					},
				},
				"contrib": {
					Name:        "foo-contrib-v2",
					Description: "Contributed features",
					Dependencies: &SubPackageDependencies{
						Runtime: PackageDependencyList{
							"openssl-libs": {},
						},
					},
					Conflicts: PackageDependencyList{
						"foo": {},
					},
					Provides: PackageDependencyList{
						"foo-contrib": {},
					},
				},
			},
		},
		{
			name: "A supplemental package without a description is rejected with quoted key context",
			packages: map[string]SubPackage{
				"": {},
			},
			wantError: `package "": description is required`,
		},
		{
			name: "A supplemental package named after the root package is rejected",
			packages: map[string]SubPackage{
				"bad": {Name: "foo", Description: "Conflicts with root"},
			},
			wantError: "conflicts with the primary package name",
		},
		{
			name: "Supplemental packages with the same custom name are rejected",
			packages: map[string]SubPackage{
				"a": {Name: "same-name", Description: "Package A"},
				"b": {Name: "same-name", Description: "Package B"},
			},
			wantError: "both resolve to the same name",
		},
		{
			name: "A custom name matching another supplemental package default name is rejected",
			packages: map[string]SubPackage{
				"debug": {Description: "Default foo-debug"},
				"other": {Name: "foo-debug", Description: "Also foo-debug"},
			},
			wantError: "both resolve to the same name",
		},
		{
			name: "A supplemental package with disable_strip enabled is rejected",
			packages: map[string]SubPackage{
				"tools": {
					Description: "Tools package",
					Artifacts: &Artifacts{
						DisableStrip: true,
					},
				},
			},
			wantError: "artifacts: disable_strip is only valid for root package artifacts",
		},
		{
			name: "A supplemental package with disable_auto_requires enabled is accepted",
			packages: map[string]SubPackage{
				"tools": {
					Description: "Tools package",
					Artifacts: &Artifacts{
						DisableAutoRequires: true,
					},
				},
			},
		},
		{
			name: "A supplemental package with strip control unset is accepted",
			packages: map[string]SubPackage{
				"tools": {
					Description: "Tools package",
					Artifacts:   &Artifacts{},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			spec := validSpecWithSubPackages(tc.packages)
			err := spec.Validate()

			if tc.wantError == "" {
				assert.NilError(t, err)
				return
			}

			assert.Check(t, err != nil, "expected an error but got nil")
			assert.Check(t, strings.Contains(err.Error(), tc.wantError),
				"expected error to contain %q, got: %s", tc.wantError, err)
		})
	}
}

func validSpecWithSubPackages(packages map[string]SubPackage) Spec {
	return Spec{
		Name:        "foo",
		Description: "Test package",
		Version:     "1.0.0",
		Revision:    "1",
		License:     "MIT",
		Website:     "https://example.com",
		Targets: map[string]Target{
			"azlinux3": {
				Packages: packages,
			},
		},
	}
}
