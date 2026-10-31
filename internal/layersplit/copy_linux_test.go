package layersplit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

// mergeInto copies each output on top of dst in order, like stacking layers.
func mergeInto(t *testing.T, dst string, outs []string) {
	t.Helper()
	for _, out := range outs {
		cmd := exec.Command("cp", "-a", out+"/.", dst+"/")
		dt, err := cmd.CombinedOutput()
		assert.NilError(t, err, string(dt))
	}
}

func TestCopyAndVerify(t *testing.T) {
	root := testRoot(t)

	// Extra entry types: hardlink, restrictive dir mode, setuid file,
	// timestamps and (when supported) an xattr.
	assert.NilError(t, os.Link(filepath.Join(root, "usr/bin/myapp"), filepath.Join(root, "usr/bin/myapp-link")))
	assert.NilError(t, os.Chmod(filepath.Join(root, "usr/share/doc"), 0o750))
	assert.NilError(t, unix.Chmod(filepath.Join(root, "usr/bin/openssl"), 0o4755))
	old := time.Unix(1_000_000, 0)
	assert.NilError(t, os.Chtimes(filepath.Join(root, "usr/lib/libssl.so.3"), old, old))
	xattrSupported := unix.Lsetxattr(filepath.Join(root, "usr/bin/myapp"), "user.dalec.test", []byte("v"), 0) == nil

	in := testInputs()
	in.Files = append(in.Files, OwnedFile{Package: "myapp", Path: "/usr/bin/myapp-link"})
	plan, err := BuildPlan(root, in, Options{})
	assert.NilError(t, err)

	base := t.TempDir()
	outs := make([]string, len(plan.Groups))
	for i := range plan.Groups {
		outs[i] = filepath.Join(base, strconv.Itoa(i))
		assert.NilError(t, os.Mkdir(outs[i], 0o755))
	}
	assert.NilError(t, Copy(root, plan.Assign, outs, 42))

	// Files only land in their own layer.
	appIdx := plan.Assign["/usr/bin/myapp"]
	_, err = os.Stat(filepath.Join(outs[appIdx], "usr/bin/myapp"))
	assert.NilError(t, err)
	_, err = os.Stat(filepath.Join(outs[appIdx], "usr/lib/libssl.so.3"))
	assert.Check(t, os.IsNotExist(err))

	// Hardlinks within a layer are preserved.
	var a, b unix.Stat_t
	assert.NilError(t, unix.Lstat(filepath.Join(outs[appIdx], "usr/bin/myapp"), &a))
	assert.NilError(t, unix.Lstat(filepath.Join(outs[appIdx], "usr/bin/myapp-link"), &b))
	assert.Check(t, cmp.Equal(a.Ino, b.Ino))

	// Parent directories keep their metadata in every layer.
	glibcIdx := plan.Assign["/usr/share/doc/glibc/NEWS"]
	fi, err := os.Stat(filepath.Join(outs[glibcIdx], "usr/share/doc"))
	assert.NilError(t, err)
	assert.Check(t, cmp.Equal(fi.Mode().Perm(), os.FileMode(0o750)))
	assert.Check(t, cmp.Equal(fi.ModTime().Unix(), int64(42)))

	merged := t.TempDir()
	mergeInto(t, merged, outs)

	want, err := Tree(root)
	assert.NilError(t, err)
	got, err := Tree(merged)
	assert.NilError(t, err)
	assert.Check(t, cmp.Len(Compare(want, got), 0))

	if xattrSupported {
		assert.Check(t, want["/usr/bin/myapp"].Xattrs != "")
	}
	assert.Check(t, cmp.Equal(want["/usr/lib/libssl.so.3"].Mtime, old.UnixNano()))
	assert.Check(t, cmp.Equal(want["/usr/bin/openssl"].Mode, uint32(0o4755)))
}

func TestCompareReportsDifferences(t *testing.T) {
	expected := t.TempDir()
	actual := t.TempDir()
	writeTree(t, expected, map[string]string{"/a": "1", "/b": "2"}, nil)
	writeTree(t, actual, map[string]string{"/a": "changed", "/c": "3"}, nil)

	want, err := Tree(expected)
	assert.NilError(t, err)
	got, err := Tree(actual)
	assert.NilError(t, err)

	diffs := Compare(want, got)
	assert.Assert(t, cmp.Len(diffs, 3))
	assert.Check(t, cmp.Equal(diffs[0].Path, "/a"))
	assert.Check(t, diffs[1].Actual == nil, "/b should be missing")
	assert.Check(t, diffs[2].Expected == nil, "/c should be unexpected")

	err = runVerify(expected, actual)
	assert.ErrorContains(t, err, "verification failed")
}
