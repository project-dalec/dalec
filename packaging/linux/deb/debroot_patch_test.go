package deb

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
	"gotest.tools/v3/assert"
)

func intPtr(i int) *int {
	return &i
}

func TestCreatePatchScript_MultiplePatchesSorted(t *testing.T) {
	t.Parallel()

	spec := &dalec.Spec{
		Patches: map[string][]dalec.PatchSpec{
			"src-b": {
				{Source: "patch-b1", Strip: intPtr(1)},
			},
			"src-a": {
				{Source: "patch-a1", Strip: intPtr(1)},
				{Source: "patch-a2", Strip: intPtr(1)},
			},
		},
	}

	got := string(createPatchScript(spec, nil))

	// Patches should be sorted by the source name being patched (the key in Patches map)
	// to ensure deterministic ordering, matching RPM behavior.
	idxA := strings.Index(got, `patch -d "src-a"`)
	idxB := strings.Index(got, `patch -d "src-b"`)

	assert.Assert(t, idxA >= 0, "expected patch command for src-a, got:\n%s", got)
	assert.Assert(t, idxB >= 0, "expected patch command for src-b, got:\n%s", got)
	assert.Assert(t, idxA < idxB, "expected src-a patches before src-b patches for deterministic ordering, got:\n%s", got)

	// Verify all patch commands are present
	assert.Assert(t, strings.Contains(got, "patch-a1"), "expected patch-a1 reference in script, got:\n%s", got)
	assert.Assert(t, strings.Contains(got, "patch-a2"), "expected patch-a2 reference in script, got:\n%s", got)
	assert.Assert(t, strings.Contains(got, "patch-b1"), "expected patch-b1 reference in script, got:\n%s", got)
}

func TestSourcePatchesDir_SeriesContainsPatchSourceNames(t *testing.T) {
	t.Parallel()

	spec := &dalec.Spec{
		Sources: map[string]dalec.Source{
			"my-patch1": {
				Inline: &dalec.SourceInline{
					File: &dalec.SourceInlineFile{
						Contents: "patch1 content",
					},
				},
			},
			"my-patch2": {
				Inline: &dalec.SourceInline{
					File: &dalec.SourceInlineFile{
						Contents: "patch2 content",
					},
				},
			},
		},
		Patches: map[string][]dalec.PatchSpec{
			"my-src": {
				{Source: "my-patch1", Strip: intPtr(1)},
				{Source: "my-patch2", Strip: intPtr(1)},
			},
		},
	}

	ctx := context.Background()
	base := llb.Scratch().File(llb.Mkdir("patches", 0o755))
	states := sourcePatchesDir(dalec.SourceOpts{}, base, "patches", "my-src", spec)

	// The last state should be the series file
	seriesState := states[len(states)-1]
	def, err := seriesState.Marshal(ctx)
	assert.NilError(t, err)

	// Find the series file in the definition and verify its contents
	mkfile, err := findMkfile(t, def.ToPB(), filepath.Join("/patches", "my-src", "series"))
	assert.NilError(t, err)
	assert.Assert(t, mkfile != nil, "series file not found in the state definition")

	// The series file should contain the patch source names, not the target name
	content := string(mkfile.Data)
	lines := strings.Split(strings.TrimSpace(content), "\n")
	assert.Assert(t, len(lines) == 2, "expected 2 lines in series file, got %d: %q", len(lines), content)
	assert.Assert(t, lines[0] == "my-patch1", "expected first line to be 'my-patch1', got %q", lines[0])
	assert.Assert(t, lines[1] == "my-patch2", "expected second line to be 'my-patch2', got %q", lines[1])

	// Verify it does NOT contain the target source name repeated
	assert.Assert(t, !bytes.Contains(mkfile.Data, []byte("my-src")),
		"series file should contain patch source names, not the target source name 'my-src', got: %q", content)
}
