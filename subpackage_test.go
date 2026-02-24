package dalec

import (
	"slices"
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

func TestSubPackageValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		pkg       SubPackage
		expectErr bool
		errSubstr string
	}{
		{
			name: "valid subpackage",
			pkg: SubPackage{
				Description: "A debug package",
				Artifacts: &Artifacts{
					Binaries: map[string]ArtifactConfig{
						"dbg/foo": {SubPath: "foo.dbg"},
					},
				},
			},
			expectErr: false,
		},
		{
			name:      "missing description",
			pkg:       SubPackage{},
			expectErr: true,
			errSubstr: "description is required",
		},
		{
			name: "valid with dependencies",
			pkg: SubPackage{
				Description: "Contrib package",
				Dependencies: &SubPackageDependencies{
					Runtime: PackageDependencyList{
						"openssl-libs": {},
					},
				},
				Conflicts: PackageDependencyList{
					"foo": {},
				},
				Provides: PackageDependencyList{
					"foo": {},
				},
			},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.pkg.validate()
			if tt.expectErr {
				assert.Check(t, err != nil, "expected an error but got nil")
				if tt.errSubstr != "" {
					assert.Check(t, cmp.ErrorContains(err, tt.errSubstr))
				}
			} else {
				assert.NilError(t, err)
			}
		})
	}
}

func TestValidateSubPackageNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		specName   string
		targetName string
		packages   map[string]SubPackage
		expectErr  bool
		errSubstr  string
	}{
		{
			name:       "nil packages",
			specName:   "foo",
			targetName: "azlinux3",
			packages:   nil,
			expectErr:  false,
		},
		{
			name:       "empty packages",
			specName:   "foo",
			targetName: "azlinux3",
			packages:   map[string]SubPackage{},
			expectErr:  false,
		},
		{
			name:       "valid packages with default names",
			specName:   "foo",
			targetName: "azlinux3",
			packages: map[string]SubPackage{
				"debug":   {Description: "debug pkg"},
				"contrib": {Description: "contrib pkg"},
			},
			expectErr: false,
		},
		{
			name:       "valid packages with explicit name",
			specName:   "foo",
			targetName: "azlinux3",
			packages: map[string]SubPackage{
				"compat": {Name: "foo-compat-v2", Description: "compat pkg"},
			},
			expectErr: false,
		},
		{
			name:       "conflicts with spec name via default naming",
			specName:   "foo",
			targetName: "azlinux3",
			packages: map[string]SubPackage{
				// This would be weird but: if specName is "foo-debug" and key is "debug"
				// then resolved = "foo-debug-debug" which doesn't conflict.
				// But if specName is "foo" and someone sets name: "foo" explicitly:
				"bad": {Name: "foo", Description: "conflicts with primary"},
			},
			expectErr: true,
			errSubstr: "conflicts with the primary package name",
		},
		{
			name:       "duplicate resolved names from explicit overrides",
			specName:   "foo",
			targetName: "azlinux3",
			packages: map[string]SubPackage{
				"a": {Name: "same-name", Description: "pkg a"},
				"b": {Name: "same-name", Description: "pkg b"},
			},
			expectErr: true,
			errSubstr: "both resolve to the same name",
		},
		{
			name:       "duplicate via default and explicit",
			specName:   "foo",
			targetName: "azlinux3",
			packages: map[string]SubPackage{
				"debug": {Description: "default foo-debug"},
				"other": {Name: "foo-debug", Description: "also foo-debug"},
			},
			expectErr: true,
			errSubstr: "both resolve to the same name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateSubPackageNames(tt.specName, tt.targetName, tt.packages)
			if tt.expectErr {
				assert.Check(t, err != nil, "expected an error but got nil")
				if tt.errSubstr != "" {
					assert.Check(t, cmp.ErrorContains(err, tt.errSubstr))
				}
			} else {
				assert.NilError(t, err)
			}
		})
	}
}

func TestSubPackageYAMLRoundTrip(t *testing.T) {
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

func TestGetSubPackages(t *testing.T) {
	t.Parallel()

	spec := &Spec{
		Name: "foo",
		Targets: map[string]Target{
			"azlinux3": {
				Packages: map[string]SubPackage{
					"debug": {Description: "debug"},
				},
			},
			"jammy": {},
		},
	}

	// Target with packages
	pkgs := spec.GetSubPackages("azlinux3")
	assert.Check(t, cmp.Equal(len(pkgs), 1))
	_, ok := pkgs["debug"]
	assert.Check(t, ok)

	// Target without packages
	pkgs = spec.GetSubPackages("jammy")
	assert.Check(t, cmp.Equal(len(pkgs), 0))

	// Non-existent target
	pkgs = spec.GetSubPackages("nonexistent")
	assert.Check(t, pkgs == nil)
}

func TestGetAllPackageNames(t *testing.T) {
	t.Parallel()

	spec := &Spec{
		Name: "foo",
		Targets: map[string]Target{
			"azlinux3": {
				Packages: map[string]SubPackage{
					"debug":   {Description: "debug"},
					"contrib": {Name: "custom-contrib", Description: "contrib"},
				},
			},
			"jammy": {},
		},
	}

	// Target with subpackages
	names := spec.GetAllPackageNames("azlinux3")
	assert.Check(t, cmp.Equal(names[0], "foo"), "primary package should be first")
	assert.Check(t, cmp.Equal(len(names), 3))
	// The remaining names should include foo-debug and custom-contrib (order may vary for map iteration)
	remaining := names[1:]
	slices.Sort(remaining)
	assert.Check(t, cmp.Equal(remaining[0], "custom-contrib"))
	assert.Check(t, cmp.Equal(remaining[1], "foo-debug"))

	// Target without subpackages
	names = spec.GetAllPackageNames("jammy")
	assert.Check(t, cmp.Equal(len(names), 1))
	assert.Check(t, cmp.Equal(names[0], "foo"))

	// Non-existent target
	names = spec.GetAllPackageNames("nonexistent")
	assert.Check(t, cmp.Equal(len(names), 1))
	assert.Check(t, cmp.Equal(names[0], "foo"))
}

func TestSubPackageDependenciesNilSafe(t *testing.T) {
	t.Parallel()

	var d *SubPackageDependencies
	assert.Check(t, d.GetRuntime() == nil)
	assert.Check(t, d.GetRecommends() == nil)
	assert.NilError(t, d.processBuildArgs(nil, nil, nil))
}

func TestSpecValidateWithSubPackages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		spec      Spec
		expectErr bool
		errSubstr string
	}{
		{
			name: "valid spec with subpackages",
			spec: Spec{
				Name:        "foo",
				Description: "test",
				Version:     "1.0.0",
				Revision:    "1",
				License:     "MIT",
				Website:     "https://example.com",
				Targets: map[string]Target{
					"azlinux3": {
						Packages: map[string]SubPackage{
							"debug":   {Description: "debug symbols"},
							"contrib": {Description: "contrib features"},
						},
					},
				},
			},
			expectErr: false,
		},
		{
			name: "subpackage missing description",
			spec: Spec{
				Name:        "foo",
				Description: "test",
				Version:     "1.0.0",
				Revision:    "1",
				License:     "MIT",
				Website:     "https://example.com",
				Targets: map[string]Target{
					"azlinux3": {
						Packages: map[string]SubPackage{
							"debug": {},
						},
					},
				},
			},
			expectErr: true,
			errSubstr: "description is required",
		},
		{
			name: "subpackage name conflicts with primary",
			spec: Spec{
				Name:        "foo",
				Description: "test",
				Version:     "1.0.0",
				Revision:    "1",
				License:     "MIT",
				Website:     "https://example.com",
				Targets: map[string]Target{
					"azlinux3": {
						Packages: map[string]SubPackage{
							"bad": {Name: "foo", Description: "conflict"},
						},
					},
				},
			},
			expectErr: true,
			errSubstr: "conflicts with the primary package name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.spec.Validate()
			if tt.expectErr {
				assert.Check(t, err != nil, "expected an error but got nil")
				if tt.errSubstr != "" {
					assert.Check(t, strings.Contains(err.Error(), tt.errSubstr),
						"expected error to contain %q, got: %s", tt.errSubstr, err)
				}
			} else {
				assert.NilError(t, err)
			}
		})
	}
}
