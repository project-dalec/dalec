package bkfs

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/tonistiigi/fsutil"
	"github.com/tonistiigi/fsutil/types"
)

var (
	_ fs.DirEntry   = (*stateRefDirEntry)(nil)
	_ fs.ReadDirFS  = (*StateRefFS)(nil)
	_ io.ReaderAt   = (*stateRefFile)(nil)
	_ fs.ReadDirFile = (*stateRefFile)(nil)
	_ fs.ReadDirFS  = (*nullFS)(nil)
)

type StateRefFS struct {
	ctx context.Context
	ref gwclient.Reference
}

func FromRef(ctx context.Context, ref gwclient.Reference) *StateRefFS {
	return &StateRefFS{
		ctx: ctx,
		ref: ref,
	}
}

func FromState(ctx context.Context, state *llb.State, client gwclient.Client, opts ...llb.ConstraintsOpt) (fs.ReadDirFS, error) {
	return fromState(ctx, state, client, false, opts...)
}

func EvalFromState(ctx context.Context, state *llb.State, client gwclient.Client, opts ...llb.ConstraintsOpt) (fs.ReadDirFS, error) {
	return fromState(ctx, state, client, true, opts...)
}

func fromState(ctx context.Context, state *llb.State, client gwclient.Client, eval bool, opts ...llb.ConstraintsOpt) (fs.ReadDirFS, error) {
	if state == nil {
		return &nullFS{}, nil
	}

	res, err := fetchRef(ctx, client, *state, eval, opts...)
	if err != nil {
		return nil, err
	}

	ref, err := res.SingleRef()
	if err != nil {
		return nil, err
	}

	return FromRef(ctx, ref), nil
}

func fetchRef(ctx context.Context, client gwclient.Client, st llb.State, eval bool, opts ...llb.ConstraintsOpt) (*gwclient.Result, error) {
	def, err := st.Marshal(ctx, opts...)
	if err != nil {
		return nil, err
	}

	res, err := client.Solve(ctx, gwclient.SolveRequest{
		Definition: def.ToPB(),
		Evaluate:   eval,
	})
	if err != nil {
		return nil, err
	}

	return res, nil
}

type stateRefDirEntry struct {
	stat *types.Stat
}

func (s *stateRefDirEntry) Name() string {
	return path.Base(s.stat.Path)
}

func (s *stateRefDirEntry) IsDir() bool {
	return s.stat.IsDir()
}

func (s *stateRefDirEntry) Type() fs.FileMode {
	return fs.FileMode(s.stat.Mode)
}

func (s *stateRefDirEntry) Info() (fs.FileInfo, error) {
	info := &fsutil.StatInfo{
		Stat: s.stat,
	}

	return info, nil
}

func (st *StateRefFS) ReadDir(name string) ([]fs.DirEntry, error) {
	contents, err := st.ref.ReadDir(st.ctx, gwclient.ReadDirRequest{
		Path: name,
	})
	if err != nil {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: err}
	}

	entries := []fs.DirEntry{}
	for _, stat := range contents {
		dirEntry := &stateRefDirEntry{stat: stat}
		entries = append(entries, dirEntry)
	}

	return entries, nil
}

type stateRefFile struct {
	eof    bool   // has file been read to EOF?
	path   string // the full path of the file from root
	ref    gwclient.Reference
	ctx    context.Context
	stat   *types.Stat
	offset int64
}

// close is a no-op
func (s *stateRefFile) Close() error {
	return nil
}

func (s *stateRefFile) ReadAt(b []byte, off int64) (int, error) {
	if off < 0 {
		return 0, &fs.PathError{Op: "read", Path: s.path, Err: fs.ErrInvalid}
	}

	if off >= s.stat.Size {
		return 0, io.EOF
	}

	segmentContents, err := s.ref.ReadFile(s.ctx, gwclient.ReadRequest{
		Filename: s.path,
		Range:    &gwclient.FileRange{Offset: int(off), Length: len(b)},
	})
	if err != nil {
		return 0, err
	}

	n := copy(b, segmentContents)

	// ReaderAt is supposed to return a descriptive error when the number of bytes read is less than
	// the length of the input buffer
	if n < len(b) {
		err = io.EOF
	}

	return n, err
}

// invariant: s.offset is the offset of the next byte to be read
func (s *stateRefFile) Read(b []byte) (int, error) {
	n, err := s.ReadAt(b, s.offset)
	s.offset += int64(n)

	return n, err
}

func (s *stateRefFile) Stat() (fs.FileInfo, error) {
	info := &fsutil.StatInfo{
		Stat: s.stat,
	}

	return info, nil
}

func (s *stateRefFile) ReadDir(n int) ([]fs.DirEntry, error) {
	contents, err := s.ref.ReadDir(s.ctx, gwclient.ReadDirRequest{
		Path: s.path,
	})
	if err != nil {
		return nil, &fs.PathError{Op: "readdir", Path: s.path, Err: err}
	}

	entries := make([]fs.DirEntry, 0, len(contents))
	for _, stat := range contents {
		entries = append(entries, &stateRefDirEntry{stat: stat})
	}

	if n > 0 && len(entries) > n {
		entries = entries[:n]
	}

	return entries, nil
}

func (st *StateRefFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Err: fs.ErrInvalid, Path: name, Op: "open"}
	}

	stat, err := st.ref.StatFile(st.ctx, gwclient.StatRequest{
		Path: name,
	})
	if err != nil {
		if strings.Contains(err.Error(), "no such file or directory") {
			err = fmt.Errorf("%w: %w", fs.ErrNotExist, err)
		}
		return nil, &fs.PathError{Err: err, Op: "open", Path: name}
	}

	f := &stateRefFile{
		path:   name,
		ref:    st.ref,
		stat:   stat,
		ctx:    st.ctx,
		eof:    false,
		offset: 0,
	}

	return f, nil
}

type nullFS struct{}

func (st *nullFS) Open(name string) (fs.File, error) {
	return nil, fmt.Errorf("nullfs: %s: %w", name, fs.ErrNotExist)
}

func (st *nullFS) ReadDir(name string) ([]fs.DirEntry, error) {
	return nil, fmt.Errorf("nullfs: %s: %w", name, fs.ErrNotExist)
}
