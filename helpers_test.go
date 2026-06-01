package dalec

import (
	"testing"

	"github.com/google/go-cmp/cmp/cmpopts"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestMergeDependencies(t *testing.T) {
	tests := []struct {
		name     string
		base     *PackageDependencies
		target   *PackageDependencies
		expected *PackageDependencies
	}{
		{
			name:     "both nil",
			base:     nil,
			target:   nil,
			expected: nil,
		},
		{
			name: "base nil",
			base: nil,
			target: &PackageDependencies{
				Build: map[string]PackageConstraints{
					"pkg1": {},
				},
			},
			expected: &PackageDependencies{
				Build: map[string]PackageConstraints{
					"pkg1": {},
				},
			},
		},
		{
			name: "target nil",
			base: &PackageDependencies{
				Runtime: map[string]PackageConstraints{
					"pkg2": {},
				},
			},
			target: nil,
			expected: &PackageDependencies{
				Runtime: map[string]PackageConstraints{
					"pkg2": {},
				},
			},
		},
		{
			name: "merge dependencies",
			base: &PackageDependencies{
				Build: map[string]PackageConstraints{
					"pkg1": {},
				},
				Runtime: map[string]PackageConstraints{
					"pkg2": {},
				},
			},
			target: &PackageDependencies{
				Build: map[string]PackageConstraints{
					"pkg3": {},
				},
				Test: map[string]PackageConstraints{"test1": {}},
			},
			expected: &PackageDependencies{
				Build: map[string]PackageConstraints{
					"pkg3": {},
				},
				Runtime: map[string]PackageConstraints{
					"pkg2": {},
				},
				Test: map[string]PackageConstraints{"test1": {}},
			},
		},
		{
			name: "custom repo in target",
			base: &PackageDependencies{
				Build: map[string]PackageConstraints{
					"pkg1": {},
				},
				Runtime: map[string]PackageConstraints{
					"pkg2": {},
				},
			},
			target: &PackageDependencies{
				ExtraRepos: []PackageRepositoryConfig{
					{
						Config: map[string]Source{
							"custom.repo": {
								HTTP: &SourceHTTP{
									URL: "my.repo.com/custom.repo",
								},
							},
						},
					},
				},
			},
			expected: &PackageDependencies{
				Build: map[string]PackageConstraints{
					"pkg1": {},
				},
				Runtime: map[string]PackageConstraints{
					"pkg2": {},
				},
				ExtraRepos: []PackageRepositoryConfig{
					{
						Config: map[string]Source{
							"custom.repo": {
								HTTP: &SourceHTTP{
									URL: "my.repo.com/custom.repo",
								},
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MergeDependencies(tt.base, tt.target)
			ignored := cmpopts.IgnoreUnexported(PackageDependencies{}, PackageConstraints{}, PackageRepositoryConfig{}, Source{}, SourceHTTP{})
			assert.Check(t, cmp.DeepEqual(tt.expected, result, ignored))
		})
	}
}
