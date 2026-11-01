package layersplit

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

// Copy copies every entry of root into the output directory of its group,
// preserving type, mode, ownership, extended attributes, timestamps and
// hardlinks within a group. Parent directories are recreated in each output
// with the metadata they have in root.
//
// Directory timestamps are set to dirMtime (seconds since the Unix epoch)
// instead of being copied: they reflect when packages were installed into a
// directory rather than the package contents, and would otherwise make
// otherwise identical layers differ between builds.
func Copy(root string, assign map[string]int, outs []string, dirMtime int64) error {
	paths := make([]string, 0, len(assign))
	for p, idx := range assign {
		if idx < 0 || idx >= len(outs) {
			return fmt.Errorf("path %s assigned to unknown layer %d", p, idx)
		}
		paths = append(paths, p)
	}
	sort.Strings(paths)

	type inode struct{ dev, ino uint64 }
	links := make([]map[inode]string, len(outs))
	created := make([]map[string]bool, len(outs))
	for i := range outs {
		links[i] = make(map[inode]string)
		created[i] = make(map[string]bool)
	}
	var dirs []dirTime

	ensureParents := func(idx int, p string) error {
		var chain []string
		for d := path.Dir(p); d != "/"; d = path.Dir(d) {
			if created[idx][d] {
				break
			}
			chain = append(chain, d)
		}
		for i := len(chain) - 1; i >= 0; i-- {
			d := chain[i]
			dt, err := copyDir(root, outs[idx], d)
			if err != nil {
				return err
			}
			created[idx][d] = true
			dirs = append(dirs, dt)
		}
		return nil
	}

	for _, p := range paths {
		idx := assign[p]
		if err := ensureParents(idx, p); err != nil {
			return err
		}

		src := filepath.Join(root, filepath.FromSlash(p))
		var st unix.Stat_t
		if err := unix.Lstat(src, &st); err != nil {
			return fmt.Errorf("stat %s: %w", p, err)
		}

		if st.Mode&unix.S_IFMT == unix.S_IFDIR {
			if created[idx][p] {
				continue
			}
			dt, err := copyDir(root, outs[idx], p)
			if err != nil {
				return err
			}
			created[idx][p] = true
			dirs = append(dirs, dt)
			continue
		}

		dst := filepath.Join(outs[idx], filepath.FromSlash(p))
		if st.Mode&unix.S_IFMT == unix.S_IFREG && st.Nlink > 1 {
			key := inode{uint64(st.Dev), st.Ino}
			if first, ok := links[idx][key]; ok {
				if err := os.Link(first, dst); err != nil {
					return fmt.Errorf("hardlink %s: %w", p, err)
				}
				continue
			}
			links[idx][key] = dst
		}

		if err := copyEntry(src, dst, &st); err != nil {
			return fmt.Errorf("copy %s: %w", p, err)
		}
	}

	// Directory timestamps change while their children are created, so set
	// them last, deepest first.
	sort.Slice(dirs, func(i, j int) bool {
		return strings.Count(dirs[i].path, "/") > strings.Count(dirs[j].path, "/")
	})
	times := []unix.Timespec{{Sec: dirMtime}, {Sec: dirMtime}}
	for _, d := range dirs {
		if err := unix.UtimesNanoAt(unix.AT_FDCWD, d.path, times, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return fmt.Errorf("set times on %s: %w", d.path, err)
		}
	}
	return nil
}

type dirTime struct {
	path string
}

func copyDir(root, out, p string) (dirTime, error) {
	src := filepath.Join(root, filepath.FromSlash(p))
	dst := filepath.Join(out, filepath.FromSlash(p))

	var st unix.Stat_t
	if err := unix.Lstat(src, &st); err != nil {
		return dirTime{}, fmt.Errorf("stat %s: %w", p, err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return dirTime{}, fmt.Errorf("parent %s is not a directory", p)
	}
	if err := os.Mkdir(dst, 0o700); err != nil {
		return dirTime{}, fmt.Errorf("mkdir %s: %w", p, err)
	}
	if err := applyMetadata(src, dst, &st, false); err != nil {
		return dirTime{}, fmt.Errorf("%s: %w", p, err)
	}
	return dirTime{path: dst}, nil
}

func copyEntry(src, dst string, st *unix.Stat_t) error {
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFREG:
		if err := copyFileContents(src, dst); err != nil {
			return err
		}
	case unix.S_IFLNK:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		if err := os.Symlink(target, dst); err != nil {
			return err
		}
	case unix.S_IFCHR, unix.S_IFBLK, unix.S_IFIFO:
		if err := unix.Mknod(dst, st.Mode, int(st.Rdev)); err != nil {
			return err
		}
	case unix.S_IFSOCK:
		// Sockets are runtime objects and cannot be stored in an image layer.
		return nil
	default:
		return fmt.Errorf("unsupported file type %o", st.Mode&unix.S_IFMT)
	}

	if err := applyMetadata(src, dst, st, st.Mode&unix.S_IFMT == unix.S_IFLNK); err != nil {
		return err
	}
	return unix.UtimesNanoAt(unix.AT_FDCWD, dst, statTimes(st), unix.AT_SYMLINK_NOFOLLOW)
}

func copyFileContents(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// applyMetadata sets ownership, mode and extended attributes. Ownership is set
// before the mode and xattrs because chown clears setuid bits and file
// capabilities.
func applyMetadata(src, dst string, st *unix.Stat_t, isLink bool) error {
	if err := unix.Lchown(dst, int(st.Uid), int(st.Gid)); err != nil {
		return fmt.Errorf("chown: %w", err)
	}
	if !isLink {
		if err := removeInheritedACLs(src, dst); err != nil {
			return err
		}
		if err := unix.Chmod(dst, st.Mode&0o7777); err != nil {
			return fmt.Errorf("chmod: %w", err)
		}
	}
	return copyXattrs(src, dst, isLink)
}

// posixACLXattrs are the extended attributes holding POSIX ACLs. A new file
// inherits them from its parent directory's default ACL.
var posixACLXattrs = []string{"system.posix_acl_access", "system.posix_acl_default"}

// removeInheritedACLs removes POSIX ACLs that dst inherited from its parent
// directory but src does not have. It runs before the mode is set, since
// removing an access ACL can otherwise leave its mask in the group bits.
func removeInheritedACLs(src, dst string) error {
	for _, name := range posixACLXattrs {
		if _, err := unix.Lgetxattr(src, name, nil); err == nil {
			continue
		}
		if _, err := unix.Lgetxattr(dst, name, nil); err != nil {
			continue
		}
		if err := unix.Lremovexattr(dst, name); err != nil {
			return fmt.Errorf("remove inherited %s: %w", name, err)
		}
	}
	return nil
}

func copyXattrs(src, dst string, isLink bool) error {
	names, err := listXattrs(src)
	if err != nil {
		if isLink && isXattrUnsupported(err) {
			return nil
		}
		return fmt.Errorf("list xattrs: %w", err)
	}
	for _, name := range names {
		val, err := getXattr(src, name)
		if err != nil {
			return fmt.Errorf("get xattr %s: %w", name, err)
		}
		if err := unix.Lsetxattr(dst, name, val, 0); err != nil {
			if isLink && isXattrUnsupported(err) {
				continue
			}
			return fmt.Errorf("set xattr %s: %w", name, err)
		}
	}
	return nil
}

func isXattrUnsupported(err error) bool {
	return errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EPERM)
}

func listXattrs(p string) ([]string, error) {
	sz, err := unix.Llistxattr(p, nil)
	if err != nil {
		return nil, err
	}
	if sz == 0 {
		return nil, nil
	}
	buf := make([]byte, sz)
	sz, err = unix.Llistxattr(p, buf)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, n := range strings.Split(string(buf[:sz]), "\x00") {
		if n != "" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names, nil
}

func getXattr(p, name string) ([]byte, error) {
	sz, err := unix.Lgetxattr(p, name, nil)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, sz)
	sz, err = unix.Lgetxattr(p, name, buf)
	if err != nil {
		return nil, err
	}
	return buf[:sz], nil
}

func statTimes(st *unix.Stat_t) []unix.Timespec {
	return []unix.Timespec{
		{Sec: st.Atim.Sec, Nsec: st.Atim.Nsec},
		{Sec: st.Mtim.Sec, Nsec: st.Mtim.Nsec},
	}
}
