package layersplit

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

// Entry describes one filesystem entry for comparison.
type Entry struct {
	Type   uint32
	Mode   uint32
	UID    uint32
	GID    uint32
	Size   int64
	SHA256 string
	Link   string
	Rdev   uint64
	Mtime  int64
	Xattrs string
}

func (e Entry) String() string {
	return fmt.Sprintf("type=%o mode=%o uid=%d gid=%d size=%d sha256=%s link=%q rdev=%d mtime=%d xattrs=%s",
		e.Type, e.Mode, e.UID, e.GID, e.Size, e.SHA256, e.Link, e.Rdev, e.Mtime, e.Xattrs)
}

// Tree walks root and returns an entry for every path below it.
// Directory timestamps are not recorded since they are not stable across
// layer extraction.
func Tree(root string) (map[string]Entry, error) {
	out := make(map[string]Entry)
	err := filepath.WalkDir(root, func(full string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, full)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		e, err := entryFor(full)
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		out["/"+filepath.ToSlash(rel)] = e
		return nil
	})
	return out, err
}

func entryFor(p string) (Entry, error) {
	var st unix.Stat_t
	if err := unix.Lstat(p, &st); err != nil {
		return Entry{}, err
	}

	e := Entry{
		Type: st.Mode & unix.S_IFMT,
		Mode: st.Mode & 0o7777,
		UID:  st.Uid,
		GID:  st.Gid,
	}

	switch e.Type {
	case unix.S_IFDIR:
	case unix.S_IFREG:
		e.Size = st.Size
		e.Mtime = st.Mtim.Nano()
		sum, err := fileSHA256(p)
		if err != nil {
			return Entry{}, err
		}
		e.SHA256 = sum
	case unix.S_IFLNK:
		e.Mtime = st.Mtim.Nano()
		link, err := os.Readlink(p)
		if err != nil {
			return Entry{}, err
		}
		e.Link = link
		// Symlink permission bits are not meaningful on Linux.
		e.Mode = 0
	case unix.S_IFCHR, unix.S_IFBLK:
		e.Rdev = st.Rdev
		e.Mtime = st.Mtim.Nano()
	default:
		e.Mtime = st.Mtim.Nano()
	}

	names, err := listXattrs(p)
	if err != nil && (e.Type != unix.S_IFLNK || !isXattrUnsupported(err)) {
		return Entry{}, err
	}
	var xs []string
	for _, name := range names {
		val, err := getXattr(p, name)
		if err != nil {
			return Entry{}, err
		}
		xs = append(xs, name+"="+hex.EncodeToString(val))
	}
	e.Xattrs = strings.Join(xs, ",")
	return e, nil
}

func fileSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ClearMtimes zeroes the recorded modification time of the given paths, so
// they are not compared by [Compare].
func ClearMtimes(tree map[string]Entry, paths map[string]bool) {
	for p := range paths {
		if e, ok := tree[p]; ok {
			e.Mtime = 0
			tree[p] = e
		}
	}
}

// Diff describes a difference between two trees.
type Diff struct {
	Path     string
	Expected *Entry
	Actual   *Entry
}

func (d Diff) String() string {
	switch {
	case d.Expected == nil:
		return fmt.Sprintf("%s: unexpected entry %s", d.Path, d.Actual)
	case d.Actual == nil:
		return fmt.Sprintf("%s: missing (expected %s)", d.Path, d.Expected)
	default:
		return fmt.Sprintf("%s: expected %s, got %s", d.Path, d.Expected, d.Actual)
	}
}

// Compare returns the differences between the expected and actual trees,
// sorted by path.
func Compare(expected, actual map[string]Entry) []Diff {
	var diffs []Diff
	for p, e := range expected {
		e := e
		a, ok := actual[p]
		if !ok {
			diffs = append(diffs, Diff{Path: p, Expected: &e})
			continue
		}
		if a != e {
			a := a
			diffs = append(diffs, Diff{Path: p, Expected: &e, Actual: &a})
		}
	}
	for p, a := range actual {
		if _, ok := expected[p]; !ok {
			a := a
			diffs = append(diffs, Diff{Path: p, Actual: &a})
		}
	}
	sort.Slice(diffs, func(i, j int) bool { return diffs[i].Path < diffs[j].Path })
	return diffs
}
