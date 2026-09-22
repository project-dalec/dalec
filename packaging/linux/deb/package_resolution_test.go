package deb

import (
	"reflect"
	"testing"

	"github.com/project-dalec/dalec"
	"gotest.tools/v3/assert"
)

func TestResolvePackages(t *testing.T) {
	subArtifacts := dalec.Artifacts{DisableAutoRequires: true}
	subRuntime := dalec.PackageDependencyList{"sub-runtime": {}}
	subRecommends := dalec.PackageDependencyList{"sub-recommends": {}}
	subReplaces := dalec.PackageDependencyList{"sub-replaces": {}}
	subConflicts := dalec.PackageDependencyList{"sub-conflicts": {}}
	subProvides := dalec.PackageDependencyList{"sub-provides": {}}

	tests := []struct {
		name     string
		spec     *dalec.Spec
		target   string
		expected []resolvedPackage
	}{
		{
			name: "the primary package is always present",
			spec: &dalec.Spec{
				Name:        "example",
				Description: "primary package",
			},
			target: "test",
			expected: []resolvedPackage{
				{
					name:        "example",
					description: "primary package",
					primary:     true,
				},
			},
		},
		{
			name: "the primary package resolves target-specific metadata",
			spec: &dalec.Spec{
				Name:        "example",
				Description: "primary package",
				Targets: map[string]dalec.Target{
					"test": {
						Artifacts: &dalec.Artifacts{DisableAutoRequires: true},
						Dependencies: &dalec.PackageDependencies{
							Runtime:    dalec.PackageDependencyList{"root-runtime": {}},
							Recommends: dalec.PackageDependencyList{"root-recommends": {}},
						},
						Replaces:  dalec.PackageDependencyList{"root-replaces": {}},
						Conflicts: dalec.PackageDependencyList{"root-conflicts": {}},
						Provides:  dalec.PackageDependencyList{"root-provides": {}},
					},
				},
			},
			target: "test",
			expected: []resolvedPackage{
				{
					name:                "example",
					description:         "primary package",
					artifacts:           dalec.Artifacts{DisableAutoRequires: true},
					runtimeDependencies: dalec.PackageDependencyList{"root-runtime": {}},
					recommends:          dalec.PackageDependencyList{"root-recommends": {}},
					replaces:            dalec.PackageDependencyList{"root-replaces": {}},
					conflicts:           dalec.PackageDependencyList{"root-conflicts": {}},
					provides:            dalec.PackageDependencyList{"root-provides": {}},
					primary:             true,
				},
			},
		},
		{
			name: "supplemental packages follow the primary in map key order",
			spec: &dalec.Spec{
				Name:        "example",
				Description: "primary package",
				Targets: map[string]dalec.Target{
					"test": {
						Packages: map[string]dalec.SubPackage{
							"zeta": {
								Description: "empty artifacts",
							},
							"alpha": {
								Name:        "custom-name",
								Description: "all metadata",
								Artifacts:   &subArtifacts,
								Dependencies: &dalec.SubPackageDependencies{
									Runtime:    subRuntime,
									Recommends: subRecommends,
								},
								Replaces:  subReplaces,
								Conflicts: subConflicts,
								Provides:  subProvides,
							},
						},
					},
				},
			},
			target: "test",
			expected: []resolvedPackage{
				{
					name:        "example",
					description: "primary package",
					primary:     true,
				},
				{
					name:                "custom-name",
					description:         "all metadata",
					artifacts:           subArtifacts,
					runtimeDependencies: subRuntime,
					recommends:          subRecommends,
					replaces:            subReplaces,
					conflicts:           subConflicts,
					provides:            subProvides,
				},
				{
					name:        "example-zeta",
					description: "empty artifacts",
				},
			},
		},
		{
			name: "supplemental packages from another target are excluded",
			spec: &dalec.Spec{
				Name: "example",
				Targets: map[string]dalec.Target{
					"other": {
						Packages: map[string]dalec.SubPackage{
							"tools": {Description: "tools"},
						},
					},
				},
			},
			target: "test",
			expected: []resolvedPackage{
				{
					name:    "example",
					primary: true,
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolved := resolvePackages(test.spec, test.target)

			assert.Assert(t, reflect.DeepEqual(resolved, test.expected), "resolved: %#v\nexpected: %#v", resolved, test.expected)
		})
	}
}
