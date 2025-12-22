package testrunner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/project-dalec/dalec"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestCheckFileExistsCommand(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file.txt")
	assert.NilError(t, os.WriteFile(file, []byte("ok"), 0o600))
	missing := filepath.Join(dir, "missing.txt")

	t.Run("file exists", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileNotExists.Cmd, file)
		assert.Assert(t, cmp.Equal(code, 2))

		err := checkFileNotExists.newNotExistsError(file, false, true)
		assert.Check(t, cmp.Contains(stderr, err.Error()))
	})

	t.Run("missing file", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileNotExists.Cmd, missing)
		assert.Check(t, cmp.Equal(code, 0))
		assert.Check(t, cmp.Equal(stderr, ""))
	})

	t.Run("expect not exists", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileNotExists.Cmd, missing)
		assert.Check(t, cmp.Equal(code, 0))
		assert.Check(t, stderr == "")
	})

	t.Run("no follow symlink", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "target.txt")
		symlink := filepath.Join(dir, "symlink.txt")

		// Create a dangling symlink at <dir>/symlink.txt
		// which points to a non-existent file <dir>/target.txt
		if err := os.Symlink(target, symlink); err != nil {
			t.Skip("skipping symlink test, could not create symlink:", err)
		}

		// The target does not exist, so should exit 0
		code, _ := runCommand(t, checkFileNotExists.Cmd, symlink)
		assert.Check(t, cmp.Equal(code, 0))

		// the symlink itself does exist, which is what we are checking here.
		// This should exit non-zero since "symlink" exists and we are not following the symlink.
		code, stderr := runCommand(t, checkFileNotExists.Cmd, "--no-follow-symlinks=true", symlink)
		assert.Check(t, cmp.Equal(code, 2))
		err := checkFileNotExists.newNotExistsError(symlink, false, true)
		assert.Check(t, cmp.Contains(stderr, err.Error()))
	})
}

func TestCheckFileExistsWithCheckLLB(t *testing.T) {
	t.Run("default flags", func(t *testing.T) {
		opts := []ValidationOpt{withTestFrontend()}

		checker := &dalec.FileCheckOutput{}
		opt := checkFileNotExists.WithCheck("/var/log/app.log", checker, opts...)
		def := definitionFromStateOption(t, opt)
		execs := execsFromDefinition(t, def)
		assert.Check(t, cmp.Len(execs, 0))
	})

	t.Run("not exist and no follow", func(t *testing.T) {
		checker := &dalec.FileCheckOutput{
			NotExist: true,
			NoFollow: true,
		}
		opt := checkFileNotExists.WithCheck("/tmp/link", checker, withTestFrontend())
		args := singleExecOp(t, definitionFromStateOption(t, opt)).GetMeta().GetArgs()
		assert.Assert(t, cmp.Len(args, 5))

		actual := args[2:]
		expect := []string{string(checkFileNotExists), "--" + noFollowFlagName + "=true", "/tmp/link"}
		assert.Check(t, cmp.DeepEqual(expect, actual))
	})
}
