//go:build unix

package testrunner

import (
	"os"

	"golang.org/x/sys/unix"
)

// mmapBuffer represents a memory-mapped file on Unix systems.
type mmapBuffer struct {
	f  *os.File
	dt []byte
}

func (mf *mmapBuffer) Close() {
	if mf.dt != nil {
		munmap(mf.dt)
	}
	_ = mf.f.Close()
}

func mmapFile(path string) (*mmapBuffer, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	stat, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}

	mf := &mmapBuffer{f: f}
	size := stat.Size()
	if size == 0 {
		mf.dt = []byte{}
		return mf, nil
	}

	dt, err := mmap(mf.f.Fd(), size)
	if err != nil {
		_ = mf.f.Close()
		return nil, err
	}

	mf.dt = dt
	return mf, nil
}

func (mf *mmapBuffer) Bytes() []byte {
	return mf.dt
}

func mmap(fd uintptr, size int64) ([]byte, error) {
	for {
		dt, err := unix.Mmap(int(fd), 0, int(size), unix.PROT_READ, unix.MAP_SHARED)
		if err == unix.EINTR {
			continue
		}
		return dt, err
	}
}

func munmap(dt []byte) {
	for {
		err := unix.Munmap(dt)
		if err == unix.EINTR {
			continue
		}
		// Any other error is ignored since there is nothing we can do about it.
		return
	}
}
