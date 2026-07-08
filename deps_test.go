package dalec

import (
	"strings"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestPackageDependencyAliasResolution(t *testing.T) {
	t.Parallel()

	t.Run("an alias referenced as a package's value resolves to the anchor's constraints", func(t *testing.T) {
		t.Parallel()
		spec := loadDepsSpec(t, `
x-c: &c
  version: [">= 1.0"]
dependencies:
  build:
    somepkg: *c
`)
		build := spec.Dependencies.Build
		assert.Assert(t, cmp.Contains(build, "somepkg"))
		assert.DeepEqual(t, build["somepkg"].Version, []string{">= 1.0"})
	})

	t.Run("a merge key inside a package map merges the anchor's packages", func(t *testing.T) {
		t.Parallel()
		spec := loadDepsSpec(t, `
x-b: &b
  somepkg:
    version: [">= 1.0"]
dependencies:
  build:
    <<: *b
    gnupg2:
`)
		build := spec.Dependencies.Build
		assert.Assert(t, cmp.Contains(build, "somepkg"))
		assert.Assert(t, cmp.Contains(build, "gnupg2"))
		assert.DeepEqual(t, build["somepkg"].Version, []string{">= 1.0"})
	})

	t.Run("a merge key at the dependencies level merges the anchor's lists", func(t *testing.T) {
		t.Parallel()
		spec := loadDepsSpec(t, `
x-d: &d
  build:
    somepkg:
      version: [">= 1.0"]
dependencies:
  <<: *d
`)
		build := spec.Dependencies.Build
		assert.Assert(t, cmp.Contains(build, "somepkg"))
		assert.DeepEqual(t, build["somepkg"].Version, []string{">= 1.0"})
	})

	t.Run("an explicit package overrides a merged package of the same name", func(t *testing.T) {
		t.Parallel()
		spec := loadDepsSpec(t, `
x-b: &b
  somepkg:
    version: [">= 1.0"]
dependencies:
  build:
    <<: *b
    somepkg:
      version: [">= 2.0"]
`)
		build := spec.Dependencies.Build
		assert.DeepEqual(t, build["somepkg"].Version, []string{">= 2.0"})
	})

	t.Run("an alias resolves for runtime dependencies", func(t *testing.T) {
		t.Parallel()
		spec := loadDepsSpec(t, `
x-c: &c
  version: [">= 1.0"]
dependencies:
  runtime:
    somepkg: *c
`)
		runtime := spec.Dependencies.Runtime
		assert.Assert(t, cmp.Contains(runtime, "somepkg"))
		assert.DeepEqual(t, runtime["somepkg"].Version, []string{">= 1.0"})
	})
}

func TestPackageDependencyListSequenceForm(t *testing.T) {
	t.Parallel()

	t.Run("a dependency list written as a sequence of package names loads every package", func(t *testing.T) {
		t.Parallel()
		spec := loadDepsSpec(t, `
dependencies:
  build:
    - somepkg
    - otherpkg
`)
		build := spec.Dependencies.Build
		assert.Assert(t, cmp.Contains(build, "somepkg"))
		assert.Assert(t, cmp.Contains(build, "otherpkg"))
	})

	t.Run("a package listed in sequence form keeps a source map pointing at its list entry", func(t *testing.T) {
		t.Parallel()
		doc := depsSpecHeader + `
dependencies:
  build:
    - somepkg
`
		spec, err := LoadSpec([]byte(doc))
		assert.NilError(t, err)

		pc := spec.Dependencies.Build["somepkg"]
		assert.Assert(t, pc._sourceMap != nil)
		assert.Equal(t, int(pc._sourceMap.pos.Start.Line), lineOf(doc, "- somepkg"))
	})
}

func TestPackageDependencyEmptySequence(t *testing.T) {
	t.Parallel()

	t.Run("an empty dependency sequence leaves the list unset rather than an empty map", func(t *testing.T) {
		t.Parallel()
		spec := loadDepsSpec(t, `
dependencies:
  build: []
`)
		assert.Assert(t, spec.Dependencies.Build == nil)
	})
}

func TestPackageDependencyDecodeErrors(t *testing.T) {
	t.Parallel()

	t.Run("a package constraint that cannot be decoded surfaces an error", func(t *testing.T) {
		t.Parallel()
		err := loadDepsSpecErr(t, `
dependencies:
  build:
    somepkg:
      version:
        not: a-list
`)
		assert.ErrorContains(t, err, "unmarshal package constraints")
	})

	t.Run("a non-string package key surfaces an error", func(t *testing.T) {
		t.Parallel()
		err := loadDepsSpecErr(t, `
dependencies:
  build:
    123:
      version: [">= 1.0"]
`)
		assert.ErrorContains(t, err, "expected string key")
	})
}

func TestPackageDependencySourceMaps(t *testing.T) {
	t.Parallel()

	// Line numbers are derived from the document text below so the assertions
	// are not tied to a hand-counted offset. `merged-pkg` is declared inside the
	// anchor; `direct-pkg` is declared directly under build via a merge key.
	doc := `name: t
version: "1.0"
revision: "1"
packager: t
vendor: t
license: MIT
description: t
x-b: &b
  merged-pkg:
    version: [">= 1.0"]
dependencies:
  build:
    <<: *b
    direct-pkg:
      version: [">= 2.0"]
`
	spec, err := LoadSpec([]byte(doc))
	assert.NilError(t, err)

	build := spec.Dependencies.Build
	assert.Assert(t, cmp.Contains(build, "merged-pkg"))
	assert.Assert(t, cmp.Contains(build, "direct-pkg"))

	t.Run("a package pulled in by a merge key maps to its anchor definition, not the merge-key line", func(t *testing.T) {
		t.Parallel()
		merged := build["merged-pkg"]
		assert.Assert(t, merged._sourceMap != nil)
		// The merged package's source map points at the anchor's value node (where
		// it is actually declared), not at the `<<: *b` merge-key line under build.
		assert.Equal(t, int(merged._sourceMap.pos.Start.Line), lineOf(doc, `version: [">= 1.0"]`))
		assert.Assert(t, int(merged._sourceMap.pos.Start.Line) != lineOf(doc, "<<: *b"))
	})

	t.Run("a directly-specified package has a source map starting at its own key line", func(t *testing.T) {
		t.Parallel()
		direct := build["direct-pkg"]
		assert.Assert(t, direct._sourceMap != nil)
		assert.Equal(t, int(direct._sourceMap.pos.Start.Line), lineOf(doc, "direct-pkg:"))
	})
}

func TestPackageDependencyAliasValueSourceMap(t *testing.T) {
	t.Parallel()

	t.Run("a package whose value is an alias keeps the anchor's source map instead of moving to its own key", func(t *testing.T) {
		t.Parallel()
		doc := depsSpecHeader + `
x-c: &c
  version: [">= 1.0"]
dependencies:
  build:
    somepkg: *c
`
		spec, err := LoadSpec([]byte(doc))
		assert.NilError(t, err)

		pc := spec.Dependencies.Build["somepkg"]
		assert.Assert(t, pc._sourceMap != nil)
		// The value is an alias to an anchor declared above the `somepkg` key, so the
		// entry's source map precedes its key. It must stay at the anchor rather than
		// being pulled down to the key line.
		assert.Equal(t, int(pc._sourceMap.pos.Start.Line), lineOf(doc, "x-c: &c"))
		assert.Assert(t, int(pc._sourceMap.pos.Start.Line) != lineOf(doc, "somepkg: *c"))
	})
}

// depsSpecHeader is a minimal valid spec header that the dependency tests prefix
// to a dependencies block so the document loads.
const depsSpecHeader = `
name: t
version: "1.0"
revision: "1"
packager: t
vendor: t
license: MIT
description: t
`

// loadDepsSpec loads a spec composed of a minimal valid header plus the given
// body (typically an anchor and a dependencies block) and fails the test if it
// cannot be loaded.
func loadDepsSpec(t *testing.T, body string) *Spec {
	t.Helper()

	spec, err := LoadSpec([]byte(depsSpecHeader + body))
	assert.NilError(t, err)
	assert.Assert(t, spec.Dependencies != nil)
	return spec
}

// loadDepsSpecErr loads a spec composed of the minimal header plus body and
// returns the resulting error for the caller to assert on.
func loadDepsSpecErr(t *testing.T, body string) error {
	t.Helper()

	_, err := LoadSpec([]byte(depsSpecHeader + body))
	return err
}

// lineOf returns the 1-based line number of the first line in doc that contains
// substr, or 0 if it is not found.
func lineOf(doc, substr string) int {
	line := 0
	for l := range strings.SplitSeq(doc, "\n") {
		line++
		if strings.Contains(l, substr) {
			return line
		}
	}
	return 0
}
