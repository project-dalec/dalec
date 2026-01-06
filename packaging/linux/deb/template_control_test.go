package deb

import (
	"reflect"
	"strings"
	"testing"

	"github.com/project-dalec/dalec"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestAppendConstraints(t *testing.T) {
	tests := []struct {
		name string
		deps map[string]dalec.PackageConstraints
		want []string
	}{
		{
			name: "nil dependencies",
			deps: nil,
			want: nil,
		},
		{
			name: "empty dependencies",
			deps: map[string]dalec.PackageConstraints{},
			want: []string{},
		},
		{
			name: "single dependency without constraints",
			deps: map[string]dalec.PackageConstraints{
				"packageA": {},
			},
			want: []string{"packageA"},
		},
		{
			name: "single dependency with version constraints",
			deps: map[string]dalec.PackageConstraints{
				"packageA": {Version: []string{">= 1.0", "<< 2.0"}},
			},
			want: []string{"packageA (<< 2.0), packageA (>= 1.0)"},
		},
		{
			name: "single dependency with architecture constraints",
			deps: map[string]dalec.PackageConstraints{
				"packageA": {Arch: []string{"amd64", "arm64"}},
			},
			want: []string{"packageA [amd64 arm64]"},
		},
		{
			name: "single dependency with version and architecture constraints",
			deps: map[string]dalec.PackageConstraints{
				"packageA": {Version: []string{">= 1.0", "<< 2.0"}, Arch: []string{"amd64", "arm64"}},
			},
			want: []string{"packageA (<< 2.0) [amd64 arm64], packageA (>= 1.0) [amd64 arm64]"},
		},
		{
			name: "multiple dependencies with constraints",
			deps: map[string]dalec.PackageConstraints{
				"packageB": {Version: []string{"= 1.0"}},
				"packageA": {Arch: []string{"amd64"}},
			},
			want: []string{"packageA [amd64]", "packageB (= 1.0)"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AppendConstraints(tt.deps); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("AppendConstraints() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestControlWrapper_ReplacesConflictsProvides(t *testing.T) {
	t.Run("target specific", func(t *testing.T) {
		spec := &dalec.Spec{
			Name:    "test-pkg",
			Version: "1.0.0",
			Targets: map[string]dalec.Target{
				"target1": {
					Replaces: map[string]dalec.PackageConstraints{
						"pkg-a": {Version: []string{">> 1.0.0"}},
					},
					Conflicts: map[string]dalec.PackageConstraints{
						"pkg-b": {Version: []string{"<< 2.0.0"}},
					},
					Provides: map[string]dalec.PackageConstraints{
						"pkg-c": {},
					},
				},
				"target2": {
					Replaces: map[string]dalec.PackageConstraints{
						"pkg-d": {Version: []string{"= 3.0.0"}},
					},
					Conflicts: map[string]dalec.PackageConstraints{
						"pkg-e": {Arch: []string{"amd64", "arm64"}},
					},
					Provides: map[string]dalec.PackageConstraints{
						"pkg-f": {Version: []string{">= 4.0.0"}},
					},
				},
			},
		}

		// Test target1
		wrapper1 := &controlWrapper{spec, "target1"}

		// Test Replaces
		replaces := wrapper1.Replaces().String()
		assert.Assert(t, cmp.Contains(replaces, "Replaces: pkg-a (>> 1.0.0)"))

		// Test Conflicts
		conflicts := wrapper1.Conflicts().String()
		assert.Assert(t, cmp.Contains(conflicts, "Conflicts: pkg-b (<< 2.0.0)"))

		// Test Provides
		provides := wrapper1.Provides().String()
		assert.Assert(t, cmp.Contains(provides, "Provides: pkg-c"))

		// Test target2
		wrapper2 := &controlWrapper{spec, "target2"}

		// Test Replaces
		replaces = wrapper2.Replaces().String()
		assert.Assert(t, cmp.Contains(replaces, "Replaces: pkg-d (= 3.0.0)"))

		// Test Conflicts
		conflicts = wrapper2.Conflicts().String()
		assert.Assert(t, cmp.Contains(conflicts, "Conflicts: pkg-e [amd64 arm64]"))

		// Test Provides
		provides = wrapper2.Provides().String()
		assert.Assert(t, cmp.Contains(provides, "Provides: pkg-f (>= 4.0.0)"))
	})

	t.Run("non-target specific", func(t *testing.T) {
		spec := &dalec.Spec{
			Name:    "test-pkg",
			Version: "1.0.0",
			Replaces: map[string]dalec.PackageConstraints{
				"pkg-g": {Version: []string{">> 1.0.0"}},
			},
			Conflicts: map[string]dalec.PackageConstraints{
				"pkg-h": {Version: []string{"<< 2.0.0"}},
			},
			Provides: map[string]dalec.PackageConstraints{
				"pkg-i": {Version: []string{">= 3.0.0"}, Arch: []string{"amd64"}},
			},
		}

		// Test with any target name
		wrapper := &controlWrapper{spec, "any-target"}

		// Test Replaces
		replaces := wrapper.Replaces().String()
		assert.Assert(t, cmp.Contains(replaces, "Replaces: pkg-g (>> 1.0.0)"))

		// Test Conflicts
		conflicts := wrapper.Conflicts().String()
		assert.Assert(t, cmp.Contains(conflicts, "Conflicts: pkg-h (<< 2.0.0)"))

		// Test Provides
		provides := wrapper.Provides().String()
		assert.Assert(t, cmp.Contains(provides, "Provides: pkg-i (>= 3.0.0) [amd64]"))
	})

	t.Run("empty values", func(t *testing.T) {
		spec := &dalec.Spec{
			Name:    "test-pkg",
			Version: "1.0.0",
			// No Replaces, Conflicts, or Provides defined
		}

		wrapper := &controlWrapper{spec, "target1"}

		// Test empty values
		assert.DeepEqual(t, wrapper.Replaces().String(), "")
		assert.DeepEqual(t, wrapper.Conflicts().String(), "")
		assert.DeepEqual(t, wrapper.Provides().String(), "")
	})

	t.Run("multiline format", func(t *testing.T) {
		spec := &dalec.Spec{
			Name:    "test-pkg",
			Version: "1.0.0",
			Replaces: map[string]dalec.PackageConstraints{
				"pkg-a": {Version: []string{">> 1.0.0"}},
				"pkg-b": {Version: []string{"<< 2.0.0"}},
				"pkg-c": {Version: []string{">= 3.0.0"}},
			},
		}

		wrapper := &controlWrapper{spec, "any-target"}
		replaces := wrapper.Replaces().String()

		// Test multiline formatting
		lines := strings.Split(strings.TrimSpace(replaces), "\n")
		assert.Equal(t, len(lines), 3)
		assert.Assert(t, cmp.Contains(lines[0], "Replaces: pkg-a (>> 1.0.0),"))
		assert.Assert(t, cmp.Contains(lines[1], "         pkg-b (<< 2.0.0),"))
		assert.Assert(t, cmp.Contains(lines[2], "         pkg-c (>= 3.0.0)"))
	})

	t.Run("target precedence", func(t *testing.T) {
		// Create spec with both root-level and target-specific values
		spec := &dalec.Spec{
			Name:    "test-pkg",
			Version: "1.0.0",
			// Root-level definitions
			Replaces: map[string]dalec.PackageConstraints{
				"root-pkg-r": {Version: []string{">= 1.0.0"}},
				"common-pkg": {Version: []string{">= 2.0.0"}}, // Will be overridden in target1
			},
			Conflicts: map[string]dalec.PackageConstraints{
				"root-pkg-c": {Version: []string{"<= 3.0.0"}},
				"common-pkg": {Version: []string{"<= 4.0.0"}}, // Will be overridden in target1
			},
			Provides: map[string]dalec.PackageConstraints{
				"root-pkg-p": {Version: []string{"= 5.0.0"}},
				"common-pkg": {Version: []string{"= 6.0.0"}}, // Will be overridden in target1
			},
			Targets: map[string]dalec.Target{
				// target1 overrides values
				"target1": {
					Replaces: map[string]dalec.PackageConstraints{
						"target-pkg-r": {Version: []string{">= 1.1.0"}},
						"common-pkg":   {Version: []string{">= 2.1.0"}}, // Overrides root
					},
					Conflicts: map[string]dalec.PackageConstraints{
						"target-pkg-c": {Version: []string{"<= 3.1.0"}},
						"common-pkg":   {Version: []string{"<= 4.1.0"}}, // Overrides root
					},
					Provides: map[string]dalec.PackageConstraints{
						"target-pkg-p": {Version: []string{"= 5.1.0"}},
						"common-pkg":   {Version: []string{"= 6.1.0"}}, // Overrides root
					},
					Artifacts: &dalec.Artifacts{
						DisableAutoRequires: true,
					},
				},
				// target2 has explicit empty maps to override the root values
				"target2": {
					Replaces:  map[string]dalec.PackageConstraints{},
					Conflicts: map[string]dalec.PackageConstraints{},
					Provides:  map[string]dalec.PackageConstraints{},
				},
			},
		}

		// Test target1 (should see target-specific values taking precedence)
		wrapper1 := &controlWrapper{spec, "target1"}

		// Test Replaces - should contain target-specific values and not root values for common-pkg
		replaces := wrapper1.Replaces().String()
		assert.Assert(t, cmp.Contains(replaces, "common-pkg (>= 2.1.0)"))
		assert.Assert(t, cmp.Contains(replaces, "target-pkg-r (>= 1.1.0)"))
		assert.Assert(t, !strings.Contains(replaces, "root-pkg-r"))
		assert.Assert(t, !strings.Contains(replaces, "(>= 2.0.0)")) // common-pkg old version

		// Test Conflicts - should contain target-specific values and not root values for common-pkg
		conflicts := wrapper1.Conflicts().String()
		assert.Assert(t, cmp.Contains(conflicts, "common-pkg (<= 4.1.0)"))
		assert.Assert(t, cmp.Contains(conflicts, "target-pkg-c (<= 3.1.0)"))
		assert.Assert(t, !strings.Contains(conflicts, "root-pkg-c"))
		assert.Assert(t, !strings.Contains(conflicts, "(<= 4.0.0)")) // common-pkg old version

		// Test Provides - should contain target-specific values and not root values for common-pkg
		provides := wrapper1.Provides().String()
		assert.Assert(t, cmp.Contains(provides, "common-pkg (= 6.1.0)"))
		assert.Assert(t, cmp.Contains(provides, "target-pkg-p (= 5.1.0)"))
		assert.Assert(t, !strings.Contains(provides, "root-pkg-p"))
		assert.Assert(t, !strings.Contains(provides, "(= 6.0.0)")) // common-pkg old version

		deps := wrapper1.AllRuntimeDeps()
		assert.Assert(t, !strings.Contains(deps.String(), "${shlibs:Depends}"))

		// Test with non-existent target to get root values
		// Current implementation only falls back to root if target doesn't exist
		wrapperNonExistent := &controlWrapper{spec, "non-existent-target"}

		// Test Replaces - should contain root values
		replaces = wrapperNonExistent.Replaces().String()
		assert.Assert(t, cmp.Contains(replaces, "common-pkg (>= 2.0.0)"))
		assert.Assert(t, cmp.Contains(replaces, "root-pkg-r (>= 1.0.0)"))

		// Test Conflicts - should contain root values
		conflicts = wrapperNonExistent.Conflicts().String()
		assert.Assert(t, cmp.Contains(conflicts, "common-pkg (<= 4.0.0)"))
		assert.Assert(t, cmp.Contains(conflicts, "root-pkg-c (<= 3.0.0)"))

		// Test Provides - should contain root values
		provides = wrapperNonExistent.Provides().String()
		assert.Assert(t, cmp.Contains(provides, "common-pkg (= 6.0.0)"))
		assert.Assert(t, cmp.Contains(provides, "root-pkg-p (= 5.0.0)"))

		// Test target2 - should return empty values because the maps are explicitly empty
		wrapper2 := &controlWrapper{spec, "target2"}
		assert.DeepEqual(t, wrapper2.Replaces().String(), "")
		assert.DeepEqual(t, wrapper2.Conflicts().String(), "")
		assert.DeepEqual(t, wrapper2.Provides().String(), "")

		deps = wrapper2.AllRuntimeDeps()
		assert.Assert(t, cmp.Contains(deps.String(), "${shlibs:Depends}"))
	})
}
