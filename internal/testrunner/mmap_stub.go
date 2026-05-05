//go:build !unix

package testrunner

import (
	"io"
	"os"
)

// mmapBuffer is a simple file-backed buffer used on non-Unix systems.
type mmapBuffer struct {
	f  *os.File
	dt []byte
}

func (mf *mmapBuffer) Close() {
	_ = mf.f.Close()
}

func mmapFile(path string) (*mmapBuffer, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	dt, err := io.ReadAll(f)
	if err != nil {
		_ = f.Close()
		return nil, err
	}

	return &mmapBuffer{f: f, dt: dt}, nil
}

func (mf *mmapBuffer) Bytes() []byte {
	return mf.dt
}
