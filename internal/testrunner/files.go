package testrunner

import (
	"bytes"
	"os"
	"strconv"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
	"golang.org/x/sys/unix"
)

func withFileChecks(test *dalec.TestSpec, opts ...ValidationOpt) []llb.StateOption {
	if len(test.Files) == 0 {
		return nil
	}

	outs := make([]llb.StateOption, 0, len(test.Files))
	for file, check := range test.Files {
		outs = append(outs, withFileCheck(file, &check, opts...)...)
	}
	return outs
}

func withFileCheck(file string, check *dalec.FileCheckOutput, opts ...ValidationOpt) []llb.StateOption {
	var outs []llb.StateOption

	outs = append(outs, checkFileExists.Validate(file, check, opts...))
	outs = append(outs, checkFileIsDir.Validate(file, check, opts...))
	outs = append(outs, checkFilePerms.Validate(file, check, opts...))
	outs = append(outs, withCheckOutput(file, &check.CheckOutput, opts...)...)

	return outs
}

// mmapBuffer represents a memory-mapped file.
// It holds the file descriptor and the mapped data.
// This is useful when reading large files for checks without loading the entire file into memory.
// Such is the case for file content checks like "contains" or "matches".
type mmapBuffer struct {
	f  *os.File
	dt []byte
}

func (mf *mmapBuffer) Close() {
	if mf.dt != nil {
		unix.Munmap(mf.dt) //nolint:errcheck
	}
	mf.f.Close() //nolint:errcheck
}

func mmapFile(path string) (*mmapBuffer, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}

	mf := &mmapBuffer{f: f}
	size := stat.Size()
	if size == 0 {
		mf.dt = []byte{}
		return mf, nil
	}

	dt, err := unix.Mmap(int(mf.f.Fd()), 0, int(size), unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		return nil, err
	}

	mf.dt = dt
	return mf, nil
}

// So yeah, this is mmmap data.
// Don't try to write to it or the gates of hell will open and come to devour us all.
func (mf *mmapBuffer) Bytes() []byte {
	return mf.dt
}

func previewString(dt []byte) string {
	if bytes.Contains(dt, []byte{'\x00'}) {
		// Don't try to print binary data.
		// The null byte check is a simple heuristic for binary data.
		// It's not perfect, but good enough for our use case.
		return "<binary data>"
	}

	// dt could be large (especially since these are all mmaped files that get passed in).
	// we don't want to pass this through entirely.
	const maxPreview = 1024
	if len(dt) > maxPreview {
		return string(dt[:maxPreview]) + "<...truncated to 1024 bytes out of " + strconv.Itoa(len(dt)) + " bytes>"
	}
	return string(dt)
}
