package layersplit

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// RemoveLinks removes the symlinks or regular files at the given paths inside
// root, so they can be created again. Paths are resolved inside root: parent
// symlinks (including absolute ones) and ".." components cannot reach outside
// of it. Missing paths and directories are left alone.
func RemoveLinks(root string, paths []string) error {
	for _, p := range paths {
		canonical, err := ResolveInRoot(root, p)
		if err != nil {
			return fmt.Errorf("resolving %s: %w", p, err)
		}
		if canonical == "/" {
			continue
		}
		full := filepath.Join(root, filepath.FromSlash(canonical))
		fi, err := os.Lstat(full)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink == 0 && !fi.Mode().IsRegular() {
			continue
		}
		if err := os.Remove(full); err != nil {
			return fmt.Errorf("removing %s: %w", p, err)
		}
	}
	return nil
}
