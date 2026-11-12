//go:build !unix

package layersplit

import "io/fs"

// inodeOf is not supported on this platform.
func inodeOf(fs.FileInfo) (dev, ino, nlink uint64, ok bool) {
	return 0, 0, 0, false
}
