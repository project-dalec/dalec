package dalec

import (
	"testing"

	"github.com/moby/buildkit/client/llb"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestRegisterGomodPatchStoresPatch(t *testing.T) {
	spec := &Spec{}

	patch := makeGomodPatchForTest("src", "gomod.patch", 2, "diff --git a/go.mod b/go.mod")
	spec.registerGomodPatch(patch)

	assert.Check(t, spec.gomodPatchesGenerated)
	got := spec.GomodPatchesForSource("src")
	assert.Check(t, cmp.Len(got, 1))
	assert.Check(t, got[0] == patch)

	spec.registerGomodPatch(nil)
	assert.Check(t, cmp.Len(spec.GomodPatchesForSource("src"), 1))
}

func TestAppendGomodPatchExtensionEntryAccumulates(t *testing.T) {
	spec := &Spec{}

	patch1 := makeGomodPatchForTest("src", "patch1.patch", 1, "patch one contents")
	patch2 := makeGomodPatchForTest("src", "patch2.patch", 3, "patch two contents")

	assert.NilError(t, spec.appendGomodPatchExtensionEntry(patch1))
	assert.NilError(t, spec.appendGomodPatchExtensionEntry(patch2))

	entries, err := spec.gomodPatchExtensionEntries()
	assert.NilError(t, err)
	assert.Check(t, cmp.Len(entries, 2))
	assert.Check(t, cmp.DeepEqual(entries[0], gomodPatchExtensionEntry{
		Source:   "src",
		FileName: "patch1.patch",
		Strip:    1,
		Contents: "patch one contents",
	}))
	assert.Check(t, cmp.DeepEqual(entries[1], gomodPatchExtensionEntry{
		Source:   "src",
		FileName: "patch2.patch",
		Strip:    3,
		Contents: "patch two contents",
	}))
}

func TestAppendGomodPatchExtensionEntrySkipsEmptyContents(t *testing.T) {
	spec := &Spec{}

	emptyPatch := &GomodPatch{SourceName: "src", FileName: "empty.patch"}
	assert.NilError(t, spec.appendGomodPatchExtensionEntry(emptyPatch))

	entries, err := spec.gomodPatchExtensionEntries()
	assert.NilError(t, err)
	assert.Check(t, entries == nil)
}

func TestPopulateGomodPatchesFromExtensionsRoundTrip(t *testing.T) {
	srcSpec := &Spec{}
	patch := makeGomodPatchForTest("src", "gomod.patch", 2, "diff --git a b")
	assert.NilError(t, srcSpec.appendGomodPatchExtensionEntry(patch))

	targetSpec := &Spec{extensions: srcSpec.extensions}
	assert.NilError(t, targetSpec.populateGomodPatchesFromExtensions())

	assert.Check(t, targetSpec.gomodPatchesGenerated)
	got := targetSpec.GomodPatchesForSource("src")
	assert.Check(t, cmp.Len(got, 1))
	assert.Check(t, got[0].FileName == "gomod.patch")
	assert.DeepEqual(t, got[0].Contents, patch.Contents)
}

func TestPopulateGomodPatchesFromExtensionsDefaultStrip(t *testing.T) {
	spec := &Spec{}
	entries := []gomodPatchExtensionEntry{{
		Source:   "src",
		FileName: "default.patch",
		Contents: "patch data",
	}}
	assert.NilError(t, spec.WithExtension(gomodPatchExtensionKey, entries))

	assert.NilError(t, spec.populateGomodPatchesFromExtensions())

	got := spec.GomodPatchesForSource("src")
	assert.Check(t, cmp.Len(got, 1))
	assert.Check(t, cmp.Equal(got[0].Strip, DefaultPatchStrip))
}

func TestPopulateGomodPatchesFromExtensionsMissingFields(t *testing.T) {
	spec := &Spec{}
	entries := []gomodPatchExtensionEntry{{
		FileName: "missing-source.patch",
		Contents: "patch data",
	}}
	assert.NilError(t, spec.WithExtension(gomodPatchExtensionKey, entries))

	err := spec.populateGomodPatchesFromExtensions()
	assert.ErrorContains(t, err, "missing source")

	entries = []gomodPatchExtensionEntry{{
		Source:   "src",
		Contents: "patch data",
	}}
	spec = &Spec{}
	assert.NilError(t, spec.WithExtension(gomodPatchExtensionKey, entries))

	err = spec.populateGomodPatchesFromExtensions()
	assert.ErrorContains(t, err, "missing filename")
}

func makeGomodPatchForTest(source, filename string, strip int, contents string) *GomodPatch {
	return &GomodPatch{
		SourceName: source,
		FileName:   filename,
		Strip:      strip,
		State:      llb.Scratch().File(llb.Mkfile(filename, 0o644, []byte(contents))),
		Contents:   []byte(contents),
	}
}
