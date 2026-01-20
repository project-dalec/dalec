package test

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"path"
	"testing"

	"github.com/project-dalec/dalec/frontend/pkg/bkfs"
	"gotest.tools/v3/assert"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

func TestStateWrapper_ReadAt(t *testing.T) {
	t.Parallel()
	ctx := startTestSpan(baseCtx, t)

	st := llb.Scratch().File(llb.Mkfile("/foo", 0644, []byte("hello world")))

	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
		rfs, err := bkfs.FromState(ctx, &st, gwc)
		assert.NilError(t, err)

		f, err := rfs.Open("foo")
		assert.NilError(t, err)

		r, ok := f.(io.ReaderAt)
		assert.Assert(t, ok)

		b := make([]byte, 11)
		n, err := r.ReadAt(b, 0)
		assert.NilError(t, err)
		assert.Equal(t, n, 11)

		b = make([]byte, 1)
		n, err = r.ReadAt(b, 11)
		assert.Equal(t, err, io.EOF)
		assert.Equal(t, n, 0)

		n, err = r.ReadAt(b, -1)
		var pe *fs.PathError
		assert.Assert(t, errors.As(err, &pe))
		assert.Equal(t, pe.Op, "read")
		assert.Equal(t, pe.Path, "foo")
		assert.Assert(t, errors.Is(pe.Err, fs.ErrInvalid))
		assert.Equal(t, n, 0)
	})
}

func TestStateWrapper_OpenInvalidPath(t *testing.T) {
	t.Parallel()
	ctx := startTestSpan(baseCtx, t)

	st := llb.Scratch().File(llb.Mkfile("/bar", 0644, []byte("hello world")))
	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
		rfs, err := bkfs.FromState(ctx, &st, gwc)
		assert.NilError(t, err)

		// cannot prefix path with "/", per go path conventions
		_, err = rfs.Open("/bar")
		if err == nil {
			t.Fatal("expected error")
		}

		assert.Assert(t, errors.Is(err, fs.ErrInvalid))
	})
}

func TestStateWrapper_Open(t *testing.T) {
	t.Parallel()
	ctx := startTestSpan(baseCtx, t)

	st := llb.Scratch().
		File(llb.Mkfile("/foo", 0644, []byte("hello world")))

	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
		fs, err := bkfs.FromState(ctx, &st, gwc)
		assert.NilError(t, err)

		f, err := fs.Open("foo")
		assert.NilError(t, err)

		b := make([]byte, 11)
		n, err := f.Read(b)
		assert.NilError(t, err)
		assert.Equal(t, n, 11)

		b = make([]byte, 1)
		n, err = f.Read(b)
		assert.Equal(t, err, io.EOF)
		assert.Equal(t, n, 0)
	})
}

func TestStateWrapper_Stat(t *testing.T) {
	t.Parallel()
	ctx := startTestSpan(baseCtx, t)

	st := llb.Scratch().File(llb.Mkfile("/foo", 0755, []byte("contents")))
	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
		rfs, err := bkfs.FromState(ctx, &st, gwc)
		assert.NilError(t, err)

		f, err := rfs.Open("foo")
		assert.NilError(t, err)

		info, err := f.Stat()
		assert.NilError(t, err)

		assert.Equal(t, info.IsDir(), false)
		assert.Equal(t, info.Mode(), fs.FileMode(0755))
		assert.Equal(t, info.Size(), int64(len([]byte("contents"))))
		assert.Equal(t, info.Name(), "foo")
	})
}

func TestStateWrapper_ReadDir(t *testing.T) {
	t.Parallel()
	ctx := startTestSpan(baseCtx, t)

	st := llb.Scratch().File(llb.Mkdir("/bar", 0644)).
		File(llb.Mkfile("/bar/foo", 0644, []byte("file contents"))).
		File(llb.Mkdir("/bar/baz", 0644))

	expectInfo := map[string]struct {
		perms    fs.FileMode
		isDir    bool
		contents []byte
	}{
		"/bar/foo": {
			perms:    0644,
			isDir:    false,
			contents: []byte("file contents"),
		},

		"/bar/baz": {
			perms: fs.ModeDir | 0644,
			isDir: true,
		},
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
		rfs, err := bkfs.FromState(ctx, &st, gwc)
		assert.NilError(t, err)

		root := "/bar"
		entries, err := rfs.ReadDir(root)
		assert.NilError(t, err)

		for _, e := range entries {
			p := path.Join(root, e.Name())
			want, ok := expectInfo[p]
			assert.Assert(t, ok)

			info, err := e.Info()
			assert.NilError(t, err)

			assert.Equal(t, want.perms, info.Mode())
			assert.Equal(t, want.perms.String(), info.Mode().String())
			assert.Equal(t, want.isDir, info.IsDir())
		}
	})
}

func TestStateWrapper_Walk(t *testing.T) {
	t.Parallel()
	ctx := startTestSpan(baseCtx, t)

	// create a simple test file structure like so:
	/*
		dir/
			a/
				b/
					ab.txt
			c/
				d/
					e/
						f123.txt
			h.exe
	*/
	st := llb.Scratch().File(llb.Mkdir("/dir", 0644)).
		File(llb.Mkdir("/dir/a", 0644)).
		File(llb.Mkdir("/dir/a/b", 0644)).
		File(llb.Mkfile("/dir/a/b/ab.txt", 0644, []byte("ab.txt contents"))).
		File(llb.Mkdir("/dir/c", 0644)).
		File(llb.Mkdir("/dir/c/d", 0644)).
		File(llb.Mkdir("/dir/c/d/e", 0644)).
		File(llb.Mkfile("/dir/c/d/e/f123.txt", 0644, []byte("f123.txt contents"))).
		File(llb.Mkfile("h.exe", 0755, []byte("h.exe contents")))

	expectInfo := map[string]struct {
		perms    fs.FileMode
		isDir    bool
		contents []byte
	}{
		"dir": {
			perms: fs.ModeDir | 0644,
			isDir: true,
		},
		"dir/a": {
			perms: fs.ModeDir | 0644,
			isDir: true,
		},
		"dir/a/b": {
			isDir: true,
			perms: fs.ModeDir | 0644,
		},
		"dir/a/b/ab.txt": {
			isDir:    false,
			perms:    0644,
			contents: []byte("ab.txt contents"),
		},
		"dir/c": {
			isDir: true,
			perms: fs.ModeDir | 0644,
		},
		"dir/c/d": {
			isDir: true,
			perms: fs.ModeDir | 0644,
		},
		"dir/c/d/e": {
			isDir: true,
			perms: fs.ModeDir | 0644,
		},
		"dir/c/d/e/f123.txt": {
			isDir:    false,
			perms:    0644,
			contents: []byte("f123.txt contents"),
		},
		"h.exe": {
			isDir:    false,
			perms:    0755,
			contents: []byte("h.exe contents"),
		},
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
		rfs, err := bkfs.FromState(ctx, &st, gwc)
		assert.NilError(t, err)
		totalCalls := 0
		err = fs.WalkDir(rfs, ".", func(currentPath string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if currentPath == "." {
				return nil
			}

			expect, ok := expectInfo[currentPath]
			assert.Assert(t, ok)

			info, err := d.Info()
			assert.NilError(t, err)
			assert.Equal(t, expect.perms, info.Mode())
			assert.Equal(t, expect.isDir, info.IsDir())

			totalCalls += 1

			if !d.IsDir() { // file
				f, err := rfs.Open(currentPath)
				assert.NilError(t, err)

				contents, err := io.ReadAll(f)
				if err != nil {
					return err
				}
				assert.DeepEqual(t, contents, expect.contents)
			}

			return nil
		})
		assert.Equal(t, len(expectInfo), totalCalls)
		assert.NilError(t, err)
	})
}

func TestStateWrapper_ReadPartial(t *testing.T) {
	t.Parallel()
	ctx := startTestSpan(baseCtx, t)

	contents := []byte(`
		This is a
		multiline
		file
	`)
	st := llb.Scratch().File(llb.Mkfile("/foo", 0644, contents))

	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
		rfs, err := bkfs.FromState(ctx, &st, gwc)
		assert.NilError(t, err)

		f, err := rfs.Open("foo")
		assert.NilError(t, err)

		// read 10 bytes
		b := make([]byte, 10)
		n, err := f.Read(b)
		assert.Equal(t, err, nil)
		assert.Equal(t, n, 10)
		assert.DeepEqual(t, b, contents[0:10])

		// read 9 bytes
		b = make([]byte, 9)
		n, err = f.Read(b)
		assert.Equal(t, err, nil)
		assert.Equal(t, n, 9)
		assert.DeepEqual(t, b, contents[10:19])

		// purposefully exceed length of remainder of file to
		// read the rest of the contents (14 bytes)
		b = make([]byte, 40)
		n, err = f.Read(b)
		assert.Equal(t, n, 14)
		assert.DeepEqual(t, b[:14], contents[19:])

		// the rest of the buffer should be an unfilled 26 bytes
		assert.DeepEqual(t, b[14:], make([]byte, 26))
		assert.Equal(t, err, io.EOF)

		// subsequent read of any size should return io.EOF
		b = make([]byte, 1)
		n, err = f.Read(b)
		assert.Equal(t, n, 0)
		assert.DeepEqual(t, b, []byte{0x0})
		assert.Equal(t, err, io.EOF)
	})
}

func TestStateWrapper_ReadAll(t *testing.T) {
	t.Parallel()
	ctx := startTestSpan(baseCtx, t)

	// purposefully exceed initial length of io.ReadAll buffer (512)
	b := make([]byte, 520)
	for i := 0; i < 520; i++ {
		b[i] = byte(i % 256)
	}

	st := llb.Scratch().File(llb.Mkfile("/file", 0644, b))

	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
		rfs, err := bkfs.FromState(ctx, &st, gwc)
		assert.NilError(t, err)

		f, err := rfs.Open("file")
		assert.NilError(t, err)

		contents, err := io.ReadAll(f)
		assert.NilError(t, err)
		assert.DeepEqual(t, contents, b)
	})
}
