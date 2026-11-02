//go:build unix

package layersplit

import (
	"io/fs"
	"syscall"
)

// inodeOf returns the device, inode number and link count of fi.
func inodeOf(fi fs.FileInfo) (dev, ino, nlink uint64, ok bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, 0, false
	}
	//nolint:unconvert // field types differ between platforms
	return uint64(st.Dev), uint64(st.Ino), uint64(st.Nlink), true
}
