package dalec

import (
	"errors"
	"testing"

	"github.com/moby/buildkit/client/llb"
	"gotest.tools/v3/assert"
)

func TestPreprocessGomodPaths(t *testing.T) {
	t.Parallel()

	t.Run("bare wildcard globs from the source root", func(t *testing.T) {
		t.Parallel()

		spec := newSpecWithGomod(&GeneratorGomod{Paths: []string{"*"}})

		var gotPattern string
		sOpt := newGlobSourceOpts(func(st llb.State, pattern string) ([]string, error) {
			gotPattern = pattern
			return []string{"foo/module1", "foo/module2"}, nil
		})

		err := spec.preprocessGomodPaths(sOpt, llb.Scratch())
		assert.NilError(t, err)

		gen := spec.Sources["foo"].Generate[0].Gomod
		assert.DeepEqual(t, gen.Paths, []string{"./module1", "./module2"})
		assert.Equal(t, gotPattern, "foo/*")
	})

	t.Run("every entry is globbed, including literal paths", func(t *testing.T) {
		t.Parallel()

		spec := newSpecWithGomod(&GeneratorGomod{Paths: []string{".", "plugins/*"}})

		var gotPatterns []string
		sOpt := newGlobSourceOpts(func(st llb.State, pattern string) ([]string, error) {
			gotPatterns = append(gotPatterns, pattern)
			switch pattern {
			case "foo":
				return []string{"foo"}, nil
			case "foo/plugins/*":
				return []string{"foo/plugins/v1"}, nil
			default:
				t.Fatalf("unexpected pattern %q", pattern)
				return nil, nil
			}
		})

		err := spec.preprocessGomodPaths(sOpt, llb.Scratch())
		assert.NilError(t, err)

		gen := spec.Sources["foo"].Generate[0].Gomod
		assert.DeepEqual(t, gen.Paths, []string{".", "./plugins/v1"})
		assert.DeepEqual(t, gotPatterns, []string{"foo", "foo/plugins/*"})
	})

	t.Run("results are fully sorted regardless of input order", func(t *testing.T) {
		t.Parallel()

		spec := newSpecWithGomod(&GeneratorGomod{Paths: []string{"moduleB/*", "moduleA/*"}})

		sOpt := newGlobSourceOpts(func(st llb.State, pattern string) ([]string, error) {
			switch pattern {
			case "foo/moduleA/*":
				return []string{"foo/moduleA/v2", "foo/moduleA/v1"}, nil
			case "foo/moduleB/*":
				return []string{"foo/moduleB/v1"}, nil
			default:
				t.Fatalf("unexpected pattern %q", pattern)
				return nil, nil
			}
		})

		err := spec.preprocessGomodPaths(sOpt, llb.Scratch())
		assert.NilError(t, err)

		gen := spec.Sources["foo"].Generate[0].Gomod
		assert.DeepEqual(t, gen.Paths, []string{"./moduleA/v1", "./moduleA/v2", "./moduleB/v1"})
	})

	t.Run("combines with the generator's Subpath", func(t *testing.T) {
		t.Parallel()

		spec := newSpecWithGomod(&GeneratorGomod{Paths: []string{"plugins/*"}})
		spec.Sources["foo"].Generate[0].Subpath = "some/nested/dir"

		var gotPattern string
		sOpt := newGlobSourceOpts(func(st llb.State, pattern string) ([]string, error) {
			gotPattern = pattern
			return []string{"foo/some/nested/dir/plugins/v1"}, nil
		})

		err := spec.preprocessGomodPaths(sOpt, llb.Scratch())
		assert.NilError(t, err)

		gen := spec.Sources["foo"].Generate[0].Gomod
		assert.DeepEqual(t, gen.Paths, []string{"./plugins/v1"})
		assert.Equal(t, gotPattern, "foo/some/nested/dir/plugins/*")
	})

	t.Run("does not touch or glob sources with no Paths", func(t *testing.T) {
		t.Parallel()

		spec := newSpecWithGomod(&GeneratorGomod{})

		sOpt := newGlobSourceOpts(func(st llb.State, pattern string) ([]string, error) {
			t.Fatal("FSGlob should not be called when Paths is empty")
			return nil, nil
		})

		err := spec.preprocessGomodPaths(sOpt, llb.Scratch())
		assert.NilError(t, err)
		assert.Assert(t, spec.Sources["foo"].Generate[0].Gomod.Paths == nil)
	})

	t.Run("is a no-op for specs without any gomod generator", func(t *testing.T) {
		t.Parallel()

		spec := &Spec{Sources: map[string]Source{"foo": {Git: &SourceGit{URL: "https://localhost/test.git", Commit: "deadbeef"}}}}

		err := spec.preprocessGomodPaths(SourceOpts{}, llb.Scratch())
		assert.NilError(t, err)
	})

	t.Run("a pattern matching nothing is silently dropped", func(t *testing.T) {
		t.Parallel()

		spec := newSpecWithGomod(&GeneratorGomod{Paths: []string{"module1/*", "typo/*"}})

		sOpt := newGlobSourceOpts(func(st llb.State, pattern string) ([]string, error) {
			if pattern == "foo/module1/*" {
				return []string{"foo/module1/v1"}, nil
			}
			return nil, nil
		})

		err := spec.preprocessGomodPaths(sOpt, llb.Scratch())
		assert.NilError(t, err)

		gen := spec.Sources["foo"].Generate[0].Gomod
		assert.DeepEqual(t, gen.Paths, []string{"./module1/v1"})
	})

	t.Run("Paths ends up a non-nil empty slice if every pattern matches nothing", func(t *testing.T) {
		t.Parallel()

		spec := newSpecWithGomod(&GeneratorGomod{Paths: []string{"typo/*"}})

		sOpt := newGlobSourceOpts(func(st llb.State, pattern string) ([]string, error) {
			return nil, nil
		})

		err := spec.preprocessGomodPaths(sOpt, llb.Scratch())
		assert.NilError(t, err)

		gen := spec.Sources["foo"].Generate[0].Gomod
		assert.Assert(t, gen.Paths != nil)
		assert.DeepEqual(t, gen.Paths, []string{})
	})

	t.Run("propagates glob errors", func(t *testing.T) {
		t.Parallel()

		spec := newSpecWithGomod(&GeneratorGomod{Paths: []string{"*"}})

		sOpt := newGlobSourceOpts(func(st llb.State, pattern string) ([]string, error) {
			return nil, errors.New("boom")
		})

		err := spec.preprocessGomodPaths(sOpt, llb.Scratch())
		assert.ErrorContains(t, err, "boom")
	})
}

func newSpecWithGomod(gen *GeneratorGomod) *Spec {
	return &Spec{
		Sources: map[string]Source{
			"foo": {
				Git: &SourceGit{
					URL:    "https://localhost/bar.git",
					Commit: "deadbeef",
				},
				Generate: []*SourceGenerator{
					{
						Gomod: gen,
					},
				},
			},
		},
	}
}

func newGlobSourceOpts(glob func(st llb.State, pattern string) ([]string, error)) SourceOpts {
	return SourceOpts{
		GetContext: func(name string, opts ...llb.LocalOption) (*llb.State, error) {
			st := llb.Local(name, opts...)
			return &st, nil
		},
		Glob: glob,
	}
}
