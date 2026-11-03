package layersplit

import (
	"net"
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
	plan, err := BuildPlan(root, in)
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

	// Parent directories have fixed metadata in lower layers, and their real
	// metadata in the top layer.
	glibcIdx := plan.Assign["/usr/share/doc/glibc/NEWS"]
	fi, err := os.Stat(filepath.Join(outs[glibcIdx], "usr/share/doc"))
	assert.NilError(t, err)
	assert.Check(t, cmp.Equal(fi.Mode().Perm(), os.FileMode(stableDirMode)))
	assert.Check(t, cmp.Equal(fi.ModTime().Unix(), int64(42)))
	fi, err = os.Stat(filepath.Join(outs[len(outs)-1], "usr/share/doc"))
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

// TestCopyLowerLayersOnlyDependOnTheirPackages checks that changing the spec
// packages, including a change they make to a directory shared with other
// packages, leaves the base and runtime dependency layers unchanged.
func TestCopyLowerLayersOnlyDependOnTheirPackages(t *testing.T) {
	split := func(t *testing.T, root string) (map[string][]string, []string) {
		t.Helper()
		plan, err := BuildPlan(root, testInputs())
		assert.NilError(t, err)

		base := t.TempDir()
		outs := make([]string, len(plan.Groups))
		for i := range plan.Groups {
			outs[i] = filepath.Join(base, strconv.Itoa(i))
			assert.NilError(t, os.Mkdir(outs[i], 0o755))
		}
		assert.NilError(t, Copy(root, plan.Assign, outs, 0))

		merged := t.TempDir()
		mergeInto(t, merged, outs)
		want, err := Tree(root)
		assert.NilError(t, err)
		got, err := Tree(merged)
		assert.NilError(t, err)
		assert.Check(t, cmp.Len(Compare(want, got), 0))

		byKind := make(map[string][]string)
		for i, g := range plan.Groups {
			byKind[g.Kind] = append(byKind[g.Kind], outs[i])
		}
		return byKind, outs
	}

	// Packages install their files with the timestamps recorded in the
	// package, so both builds have the same timestamps.
	pinTimes := func(t *testing.T, root string) string {
		t.Helper()
		ts := []unix.Timespec{{Sec: 1_000_000}, {Sec: 1_000_000}}
		err := filepath.WalkDir(root, func(p string, _ os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			return unix.UtimesNanoAt(unix.AT_FDCWD, p, ts, unix.AT_SYMLINK_NOFOLLOW)
		})
		assert.NilError(t, err)
		return root
	}

	before, _ := split(t, pinTimes(t, testRoot(t)))

	// A new version of the spec package with different contents, whose
	// install also changed the mode of /usr/bin, a directory holding files of
	// several sets.
	root := testRoot(t)
	assert.NilError(t, os.WriteFile(filepath.Join(root, "usr/bin/myapp"), []byte("myapp v2"), 0o644))
	assert.NilError(t, os.Chmod(filepath.Join(root, "usr/bin"), 0o775))
	// A directory owned by a base package, changed by an install script
	// without the package manager noticing.
	assert.NilError(t, os.Chmod(filepath.Join(root, "usr/share/doc/glibc"), 0o700))
	after, _ := split(t, pinTimes(t, root))

	for _, kind := range []string{KindBase, KindDeps} {
		assert.Assert(t, cmp.Len(before[kind], 1))
		assert.Assert(t, cmp.Len(after[kind], 1))
		want, err := Tree(before[kind][0])
		assert.NilError(t, err)
		got, err := Tree(after[kind][0])
		assert.NilError(t, err)
		assert.Check(t, cmp.Len(Compare(want, got), 0), "%s layer changed", kind)
	}
}

func TestCopyAndVerifySkipSockets(t *testing.T) {
	root := testRoot(t)
	l, err := net.Listen("unix", filepath.Join(root, "etc", "app.sock"))
	if err != nil {
		t.Skipf("unix sockets are not supported here: %v", err)
	}
	defer l.Close()

	plan, err := BuildPlan(root, testInputs())
	assert.NilError(t, err)
	base := t.TempDir()
	outs := make([]string, len(plan.Groups))
	for i := range plan.Groups {
		outs[i] = filepath.Join(base, strconv.Itoa(i))
		assert.NilError(t, os.Mkdir(outs[i], 0o755))
	}
	assert.NilError(t, Copy(root, plan.Assign, outs, 0))

	merged := t.TempDir()
	mergeInto(t, merged, outs)
	want, err := Tree(root)
	assert.NilError(t, err)
	_, ok := want["/etc/app.sock"]
	assert.Check(t, !ok, "sockets are not part of the tree")
	got, err := Tree(merged)
	assert.NilError(t, err)
	assert.Check(t, cmp.Len(Compare(want, got), 0))
}

// TestCopyKeepsSpecialHardlinksTogether checks hardlinks of non-regular
// files, which have to stay linked like regular files.
func TestCopyKeepsSpecialHardlinksTogether(t *testing.T) {
	root := testRoot(t)
	fifo := filepath.Join(root, "usr/bin/openssl.fifo")
	if err := unix.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("FIFOs are not supported here: %v", err)
	}
	assert.NilError(t, os.Link(fifo, filepath.Join(root, "usr/bin/myapp.fifo")))
	assert.NilError(t, os.Symlink("openssl", filepath.Join(root, "usr/bin/openssl.link")))
	// os.Link does not follow symlinks, so this links the symlink itself.
	assert.NilError(t, os.Link(filepath.Join(root, "usr/bin/openssl.link"), filepath.Join(root, "usr/bin/myapp.link")))

	in := testInputs()
	in.Files = append(in.Files,
		OwnedFile{Package: "openssl", Path: "/usr/bin/openssl.fifo"},
		OwnedFile{Package: "myapp", Path: "/usr/bin/myapp.fifo"},
		OwnedFile{Package: "openssl", Path: "/usr/bin/openssl.link"},
		OwnedFile{Package: "myapp", Path: "/usr/bin/myapp.link"},
	)
	plan, err := BuildPlan(root, in)
	assert.NilError(t, err)
	for _, p := range []string{"/usr/bin/openssl.fifo", "/usr/bin/myapp.fifo", "/usr/bin/openssl.link", "/usr/bin/myapp.link"} {
		assert.Check(t, cmp.Equal(groupOf(t, plan, p), KindState), p)
	}

	base := t.TempDir()
	outs := make([]string, len(plan.Groups))
	for i := range plan.Groups {
		outs[i] = filepath.Join(base, strconv.Itoa(i))
		assert.NilError(t, os.Mkdir(outs[i], 0o755))
	}
	assert.NilError(t, Copy(root, plan.Assign, outs, 0))

	merged := t.TempDir()
	mergeInto(t, merged, outs)
	want, err := Tree(root)
	assert.NilError(t, err)
	assert.Check(t, want["/usr/bin/myapp.fifo"].Hardlink != "")
	assert.Check(t, want["/usr/bin/myapp.link"].Hardlink != "")
	got, err := Tree(merged)
	assert.NilError(t, err)
	assert.Check(t, cmp.Len(Compare(want, got), 0))

	// A broken FIFO hardlink is reported.
	broken := t.TempDir()
	assert.NilError(t, unix.Mkfifo(filepath.Join(broken, "a"), 0o644))
	assert.NilError(t, unix.Mkfifo(filepath.Join(broken, "b"), 0o644))
	linked := t.TempDir()
	assert.NilError(t, unix.Mkfifo(filepath.Join(linked, "a"), 0o644))
	assert.NilError(t, os.Link(filepath.Join(linked, "a"), filepath.Join(linked, "b")))
	want, err = Tree(linked)
	assert.NilError(t, err)
	got, err = Tree(broken)
	assert.NilError(t, err)
	assert.Check(t, len(Compare(want, got)) > 0)
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

func TestCompareDetectsBrokenHardlinks(t *testing.T) {
	expected := t.TempDir()
	actual := t.TempDir()
	writeTree(t, expected, map[string]string{"/a": "same"}, nil)
	assert.NilError(t, os.Link(filepath.Join(expected, "a"), filepath.Join(expected, "b")))
	writeTree(t, actual, map[string]string{"/a": "same", "/b": "same"}, nil)
	for _, root := range []string{expected, actual} {
		for _, p := range []string{"a", "b"} {
			assert.NilError(t, os.Chtimes(filepath.Join(root, p), time.Unix(1, 0), time.Unix(1, 0)))
		}
	}

	want, err := Tree(expected)
	assert.NilError(t, err)
	got, err := Tree(actual)
	assert.NilError(t, err)
	assert.Check(t, cmp.Equal(want["/b"].Hardlink, "/a"))
	assert.Check(t, cmp.Len(Compare(want, got), 2))
}

func TestCopyDoesNotAddInheritedACLs(t *testing.T) {
	setfacl, err := exec.LookPath("setfacl")
	if err != nil {
		t.Skip("setfacl is not available")
	}

	root := t.TempDir()
	writeTree(t, root, map[string]string{"/data/file": "x"}, nil)
	// The directory gets a default ACL after its file was created, so the
	// file has no ACL of its own.
	if out, err := exec.Command(setfacl, "-d", "-m", "u::rwx,g::r-x,o::---", filepath.Join(root, "data")).CombinedOutput(); err != nil {
		t.Skipf("ACLs are not supported here: %v: %s", err, out)
	}

	in := Inputs{
		Packages: map[string]bool{"pkg": true},
		Files:    []OwnedFile{{Package: "pkg", Path: "/data/file"}, {Package: "pkg", Path: "/data"}},
	}
	plan, err := BuildPlan(root, in)
	assert.NilError(t, err)

	base := t.TempDir()
	outs := make([]string, len(plan.Groups))
	for i := range plan.Groups {
		outs[i] = filepath.Join(base, strconv.Itoa(i))
		assert.NilError(t, os.Mkdir(outs[i], 0o755))
	}
	assert.NilError(t, Copy(root, plan.Assign, outs, 0))

	idx := plan.Assign["/data/file"]
	_, err = unix.Lgetxattr(filepath.Join(outs[idx], "data/file"), "system.posix_acl_access", nil)
	assert.Check(t, err != nil, "copied file must not inherit the directory's default ACL")
	// Directories get their real metadata in the top layer.
	_, err = unix.Lgetxattr(filepath.Join(outs[len(outs)-1], "data"), "system.posix_acl_default", nil)
	assert.NilError(t, err, "the directory keeps its own default ACL")

	// The copy verifies against the original.
	merged := t.TempDir()
	mergeInto(t, merged, outs)
	want, err := Tree(root)
	assert.NilError(t, err)
	got, err := Tree(merged)
	assert.NilError(t, err)
	assert.Check(t, cmp.Len(Compare(want, got), 0))
}
