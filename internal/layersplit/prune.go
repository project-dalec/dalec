package layersplit

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"syscall"
)

// PruneTools removes from root the entries that only came from tools.
//
// root is a root filesystem that was produced by running a command on lower
// (the root filesystem being updated) merged on top of tools (a tools image
// that provided the programs to run the command). Entries of tools which
// lower does not have were only visible to the command and are removed from
// root, unless the command changed them (for example a maintainer script
// writing a file the tools image also has) or they are listed in keep (for
// example because a package installed by the command owns them). Entries
// which neither lower nor tools have were created by the command and are left
// alone.
//
// keep paths are resolved inside root. Directories are only removed when they
// are empty.
func PruneTools(root, lower, tools string, keep []string) error {
	kept := make(map[string]bool, len(keep))
	for _, p := range keep {
		r, err := ResolveInRoot(root, p)
		if err != nil {
			return fmt.Errorf("resolving %s: %w", p, err)
		}
		kept[r] = true
	}

	var dirs []string
	err := filepath.WalkDir(tools, func(full string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(tools, full)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		p := "/" + filepath.ToSlash(rel)

		inLower, err := lstatInTree(lower, p)
		if err != nil {
			return err
		}
		switch inLower {
		case entryHidden:
			// lower replaced a parent directory with something else, which
			// hides the whole tools subtree.
			return fs.SkipDir
		case entryExists:
			if lfi, err := os.Lstat(filepath.Join(lower, filepath.FromSlash(p))); err == nil && !lfi.IsDir() && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		inRoot, err := lstatInTree(root, p)
		if err != nil {
			return err
		}
		switch inRoot {
		case entryHidden:
			return fs.SkipDir
		case entryMissing:
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if kept[p] {
			return nil
		}

		target := filepath.Join(root, filepath.FromSlash(p))
		fi, err := os.Lstat(target)
		if err != nil {
			return err
		}
		if fi.IsDir() {
			dirs = append(dirs, p)
			return nil
		}
		tfi, err := d.Info()
		if err != nil {
			return err
		}
		if changed, err := entryChanged(tfi, full, fi, target); err != nil || changed {
			return err
		}
		if err := os.Remove(target); err != nil {
			return fmt.Errorf("removing %s: %w", p, err)
		}
		if d.IsDir() {
			// root has a non-directory where tools had a directory: nothing
			// below it came from tools.
			return fs.SkipDir
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Remove the directories that are left empty, deepest first.
	slices.SortFunc(dirs, func(a, b string) int { return len(b) - len(a) })
	for _, p := range dirs {
		err := os.Remove(filepath.Join(root, filepath.FromSlash(p)))
		if err == nil || errors.Is(err, fs.ErrNotExist) || isNotEmpty(err) {
			continue
		}
		return fmt.Errorf("removing %s: %w", p, err)
	}
	return nil
}

// entryChanged reports whether the entry b (at path pb) differs from a (at
// path pa) in type, permissions, ownership, size, modification time or link
// target. Rewriting a file, even with the same contents, changes its
// modification time.
func entryChanged(a fs.FileInfo, pa string, b fs.FileInfo, pb string) (bool, error) {
	if a.Mode() != b.Mode() || a.Size() != b.Size() || !a.ModTime().Equal(b.ModTime()) {
		return true, nil
	}
	if au, ag, ok := ownerOf(a); ok {
		bu, bg, _ := ownerOf(b)
		if au != bu || ag != bg {
			return true, nil
		}
	}
	if a.Mode()&fs.ModeSymlink != 0 {
		la, err := os.Readlink(pa)
		if err != nil {
			return false, err
		}
		lb, err := os.Readlink(pb)
		if err != nil {
			return false, err
		}
		return la != lb, nil
	}
	return false, nil
}

type entryState int

const (
	entryMissing entryState = iota
	entryExists
	// entryHidden means a parent of the path is not a directory.
	entryHidden
)

// lstatInTree reports whether the absolute path p exists in the tree at base
// without following symlinks in any component.
func lstatInTree(base, p string) (entryState, error) {
	cur := base
	parts := splitPath(p)
	for i, part := range parts {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) {
			return entryMissing, nil
		}
		if err != nil {
			return entryMissing, err
		}
		if i < len(parts)-1 && !fi.IsDir() {
			return entryHidden, nil
		}
	}
	return entryExists, nil
}

func isNotEmpty(err error) bool {
	return errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST)
}
