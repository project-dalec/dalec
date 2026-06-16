package dalec

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/solver/pb"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func marshalToOps(ctx context.Context, t *testing.T, st llb.State) []*pb.Op {
	t.Helper()

	def, err := st.Marshal(ctx)
	assert.NilError(t, err)

	ops := make([]*pb.Op, 0, len(def.Def))
	// The last entry is the synthetic "return" op which has no real operation.
	for _, dt := range def.Def[:len(def.Def)-1] {
		var op pb.Op
		assert.NilError(t, op.Unmarshal(dt))
		ops = append(ops, &op)
	}
	return ops
}

func collectSymlinkActions(ops []*pb.Op) (map[string]*pb.FileActionSymlink, map[string]struct{}) {
	symlinks := map[string]*pb.FileActionSymlink{}
	mkdirs := map[string]struct{}{}

	for _, op := range ops {
		fileOp := op.GetFile()
		if fileOp == nil {
			continue
		}
		for _, action := range fileOp.GetActions() {
			if sl := action.GetSymlink(); sl != nil {
				symlinks[sl.GetNewpath()] = sl
			}
			if md := action.GetMkdir(); md != nil {
				mkdirs[md.GetPath()] = struct{}{}
			}
		}
	}
	return symlinks, mkdirs
}

func hasExecOp(ops []*pb.Op) bool {
	for _, op := range ops {
		if op.GetExec() != nil {
			return true
		}
	}
	return false
}

func execScript(ctx context.Context, t *testing.T, st llb.State) string {
	t.Helper()

	for _, op := range marshalToOps(ctx, t, st) {
		fileOp := op.GetFile()
		if fileOp == nil {
			continue
		}
		for _, action := range fileOp.GetActions() {
			if mf := action.GetMkfile(); mf != nil {
				return string(mf.GetData())
			}
		}
	}

	t.Fatal("no script file was created by the exec fallback")
	return ""
}

func execMountDests(ctx context.Context, t *testing.T, st llb.State) []string {
	t.Helper()

	for _, op := range marshalToOps(ctx, t, st) {
		exec := op.GetExec()
		if exec == nil {
			continue
		}
		dests := make([]string, 0, len(exec.GetMounts()))
		for _, m := range exec.GetMounts() {
			dests = append(dests, m.GetDest())
		}
		return dests
	}

	t.Fatal("exec fallback did not emit an exec op")
	return nil
}

// ownershipCommandsFor returns the chown/chgrp script lines that target the
// given rootfs path, in the order they appear.
func ownershipCommandsFor(script, rootfsPath string) []string {
	suffix := `"` + rootfsPath + `"`

	var cmds []string
	for _, line := range strings.Split(script, "\n") {
		if !strings.HasSuffix(line, suffix) {
			continue
		}
		if strings.HasPrefix(line, "chown ") || strings.HasPrefix(line, "chgrp ") {
			cmds = append(cmds, line)
		}
	}
	return cmds
}

func testSymlinkPostInstall() *PostInstall {
	return &PostInstall{
		Symlinks: map[string]SymlinkTarget{
			"/usr/bin/src1": {Paths: []string{"/src1"}, User: "need"},
			"/usr/bin/src2": {Paths: []string{"/non/existing/dir/src2"}, Group: "coffee"},
			"/usr/bin/src3": {Paths: []string{"/non/existing/dir/src3", "/non/existing/dir2/src3"}, User: "need", Group: "coffee"},
			"/usr/bin/src4": {Paths: []string{"/plain"}},
		},
	}
}

func TestInstallPostSymlinksNative(t *testing.T) {
	ctx := context.Background()

	st := installPostSymlinksNative(testSymlinkPostInstall(), llb.Scratch())
	ops := marshalToOps(ctx, t, st)

	assert.Check(t, !hasExecOp(ops), "native symlink path should not emit an exec op")

	symlinks, mkdirs := collectSymlinkActions(ops)

	// Every newpath should produce a symlink action pointing at the right oldpath.
	assert.Check(t, cmp.Equal(len(symlinks), 5))
	assertSymlinkTarget(t, symlinks, "/src1", "/usr/bin/src1")
	assertSymlinkTarget(t, symlinks, "/non/existing/dir/src2", "/usr/bin/src2")
	assertSymlinkTarget(t, symlinks, "/non/existing/dir/src3", "/usr/bin/src3")
	assertSymlinkTarget(t, symlinks, "/non/existing/dir2/src3", "/usr/bin/src3")
	assertSymlinkTarget(t, symlinks, "/plain", "/usr/bin/src4")

	// Parent directories that don't already exist must be created, but the root
	// directory should never be created.
	assert.Check(t, cmp.Contains(mkdirs, "/non/existing/dir"))
	assert.Check(t, cmp.Contains(mkdirs, "/non/existing/dir2"))
	_, hasRoot := mkdirs["/"]
	assert.Check(t, !hasRoot, "root directory should not be created")

	// User only: uid by name, gid forced to root (0) to match the exec fallback.
	assertOwnerByName(t, symlinks["/src1"].GetOwner().GetUser(), "need")
	assertOwnerByID(t, symlinks["/src1"].GetOwner().GetGroup(), 0)

	// Group only: uid forced to root (0), gid by name.
	assertOwnerByID(t, symlinks["/non/existing/dir/src2"].GetOwner().GetUser(), 0)
	assertOwnerByName(t, symlinks["/non/existing/dir/src2"].GetOwner().GetGroup(), "coffee")

	// Both set: uid and gid both by name.
	for _, p := range []string{"/non/existing/dir/src3", "/non/existing/dir2/src3"} {
		assertOwnerByName(t, symlinks[p].GetOwner().GetUser(), "need")
		assertOwnerByName(t, symlinks[p].GetOwner().GetGroup(), "coffee")
	}

	// No ownership: no chown should be applied at all.
	assert.Check(t, cmp.Nil(symlinks["/plain"].GetOwner()))
}

func TestInstallPostSymlinksExecFallback(t *testing.T) {
	ctx := context.Background()
	worker := llb.Image("busybox")

	build := func() llb.State {
		return installPostSymlinksExec(testSymlinkPostInstall(), worker, llb.Scratch())
	}

	t.Run("symlinks are created by a worker exec, not native file actions", func(t *testing.T) {
		ops := marshalToOps(ctx, t, build())

		assert.Check(t, hasExecOp(ops), "exec fallback should emit an exec op")
		symlinks, _ := collectSymlinkActions(ops)
		assert.Check(t, cmp.Equal(len(symlinks), 0), "exec fallback should not emit native symlink actions")
	})

	t.Run("the generated script", func(t *testing.T) {
		script := execScript(ctx, t, build())

		t.Run("links every path to its source target", func(t *testing.T) {
			assert.Check(t, cmp.Contains(script, `ln -s "/usr/bin/src1" "/tmp/rootfs/src1"`))
			assert.Check(t, cmp.Contains(script, `ln -s "/usr/bin/src2" "/tmp/rootfs/non/existing/dir/src2"`))
			assert.Check(t, cmp.Contains(script, `ln -s "/usr/bin/src3" "/tmp/rootfs/non/existing/dir/src3"`))
			assert.Check(t, cmp.Contains(script, `ln -s "/usr/bin/src3" "/tmp/rootfs/non/existing/dir2/src3"`))
			assert.Check(t, cmp.Contains(script, `ln -s "/usr/bin/src4" "/tmp/rootfs/plain"`))
		})

		t.Run("creates missing parent directories before linking", func(t *testing.T) {
			assert.Check(t, cmp.Contains(script, `mkdir -p "/tmp/rootfs/non/existing/dir"`))
			assert.Check(t, cmp.Contains(script, `mkdir -p "/tmp/rootfs/non/existing/dir2"`))
		})

		t.Run("chowns a link that sets only a user and leaves the group as root", func(t *testing.T) {
			assert.Check(t, cmp.DeepEqual(ownershipCommandsFor(script, "/tmp/rootfs/src1"),
				[]string{`chown -h need "/tmp/rootfs/src1"`}))
		})

		t.Run("chgrps a link that sets only a group and leaves the user as root", func(t *testing.T) {
			assert.Check(t, cmp.DeepEqual(ownershipCommandsFor(script, "/tmp/rootfs/non/existing/dir/src2"),
				[]string{`chgrp -h coffee "/tmp/rootfs/non/existing/dir/src2"`}))
		})

		t.Run("chowns and chgrps a link that sets both", func(t *testing.T) {
			assert.Check(t, cmp.DeepEqual(ownershipCommandsFor(script, "/tmp/rootfs/non/existing/dir/src3"),
				[]string{
					`chown -h need "/tmp/rootfs/non/existing/dir/src3"`,
					`chgrp -h coffee "/tmp/rootfs/non/existing/dir/src3"`,
				}))
			assert.Check(t, cmp.DeepEqual(ownershipCommandsFor(script, "/tmp/rootfs/non/existing/dir2/src3"),
				[]string{
					`chown -h need "/tmp/rootfs/non/existing/dir2/src3"`,
					`chgrp -h coffee "/tmp/rootfs/non/existing/dir2/src3"`,
				}))
		})

		t.Run("does not change ownership of a link that sets neither", func(t *testing.T) {
			assert.Check(t, cmp.Len(ownershipCommandsFor(script, "/tmp/rootfs/plain"), 0))
		})
	})

	t.Run("passwd and group", func(t *testing.T) {
		t.Run("are mounted when a link configures ownership", func(t *testing.T) {
			dests := execMountDests(ctx, t, build())
			assert.Check(t, slices.Contains(dests, "/etc/passwd"), "expected /etc/passwd mount, got %v", dests)
			assert.Check(t, slices.Contains(dests, "/etc/group"), "expected /etc/group mount, got %v", dests)
		})

		t.Run("are not mounted when no link configures ownership", func(t *testing.T) {
			post := &PostInstall{
				Symlinks: map[string]SymlinkTarget{
					"/usr/bin/src4": {Paths: []string{"/plain"}},
				},
			}
			dests := execMountDests(ctx, t, installPostSymlinksExec(post, worker, llb.Scratch()))
			assert.Check(t, !slices.Contains(dests, "/etc/passwd"), "unexpected /etc/passwd mount in %v", dests)
			assert.Check(t, !slices.Contains(dests, "/etc/group"), "unexpected /etc/group mount in %v", dests)
		})
	})
}

func TestInstallPostSymlinksDefaultsToNative(t *testing.T) {
	ctx := context.Background()

	// The exported entrypoint should use the native path by default (symlink
	// support enabled), i.e. without an exec op.
	st := llb.Scratch().With(InstallPostSymlinks(testSymlinkPostInstall(), llb.Scratch()))
	ops := marshalToOps(ctx, t, st)

	assert.Check(t, !hasExecOp(ops), "default path should be native (no exec op)")
	symlinks, _ := collectSymlinkActions(ops)
	assert.Check(t, cmp.Equal(len(symlinks), 5))
}

func assertSymlinkTarget(t *testing.T, symlinks map[string]*pb.FileActionSymlink, newpath, oldpath string) {
	t.Helper()
	sl, ok := symlinks[newpath]
	assert.Assert(t, ok, "missing symlink for %q", newpath)
	assert.Check(t, cmp.Equal(sl.GetOldpath(), oldpath))
}

func assertOwnerByName(t *testing.T, u *pb.UserOpt, name string) {
	t.Helper()
	assert.Assert(t, u != nil)
	byName := u.GetByName()
	assert.Assert(t, byName != nil, "expected user/group by name %q, got %v", name, u)
	assert.Check(t, cmp.Equal(byName.GetName(), name))
}

func assertOwnerByID(t *testing.T, u *pb.UserOpt, id uint32) {
	t.Helper()
	assert.Assert(t, u != nil)
	_, isName := u.GetUser().(*pb.UserOpt_ByName)
	assert.Check(t, !isName, "expected user/group by id %d, got name", id)
	assert.Check(t, cmp.Equal(u.GetByID(), id))
}
