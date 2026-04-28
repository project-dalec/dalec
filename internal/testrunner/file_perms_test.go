package testrunner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestCheckFilePermsCommand(t *testing.T) {
	file := filepath.Join(t.TempDir(), "script.sh")
	assert.NilError(t, os.WriteFile(file, []byte("#!/bin/true"), 0o700))
	assert.NilError(t, os.Chmod(file, 0o764))

	t.Run("expected perms", func(t *testing.T) {
		code, stderr := runCommand(t, checkFilePerms.Cmd, file, "764")
		assert.Check(t, cmp.Equal(code, 0))
		assert.Check(t, cmp.Equal(stderr, ""))
	})

	t.Run("wrong perms", func(t *testing.T) {
		code, stderr := runCommand(t, checkFilePerms.Cmd, file, "644")
		assert.Check(t, cmp.Equal(code, 2))
		assert.Check(t, cmp.Contains(stderr, "expected: \"-rw-r--r--\""))
	})

	t.Run("symlink respects follow flag", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "target")
		assert.NilError(t, os.WriteFile(target, []byte("data"), 0o600))
		link := filepath.Join(dir, "link")
		if err := os.Symlink(target, link); err != nil {
			t.Skip("symlinks unsupported:", err)
		}

		code, stderr := runCommand(t, checkFilePerms.Cmd, link, "600")
		assert.Check(t, cmp.Equal(code, 0))
		assert.Check(t, cmp.Equal(stderr, ""))

		code, stderr = runCommand(t, checkFilePerms.Cmd, "--"+noFollowFlagName+"=true", link, "600")
		assert.Check(t, cmp.Equal(code, 2))
		assert.Check(t, cmp.Contains(stderr, "expected: \"-rw-------\""))
		gotSymlinkMode := strings.Contains(stderr, "got \"Lrwxrwxrwx\"") || strings.Contains(stderr, "got \"-rwxrwxrwx\"")
		assert.Check(t, gotSymlinkMode, "stderr did not report symlink mode: %s", stderr)
	})
}

func TestCheckFilePermsWithCheckLLB(t *testing.T) {
	t.Run("runs when permissions set", func(t *testing.T) {
		checker := fileCheckFromYAML(t, "permissions: 0o754\n")
		exec := singleExecOp(t, definitionFromStateOption(t, checkFilePerms.WithCheck("/bin/tool", checker, withTestFrontend())))
		expect := []string{
			frontendMountPath,
			testRunnerCmdName,
			string(checkFilePerms),
			"--" + noFollowFlagName + "=false",
			"/bin/tool",
			fmt.Sprintf("%o", checker.Permissions.Perm()),
		}
		assert.Check(t, cmp.DeepEqual(expect, exec.GetMeta().GetArgs()))
	})

	t.Run("no follow flag", func(t *testing.T) {
		checker := fileCheckFromYAML(t, "permissions: 0o644\nno_follow: true\n")
		exec := singleExecOp(t, definitionFromStateOption(t, checkFilePerms.WithCheck("/bin/tool", checker, withTestFrontend())))
		assert.Check(t, cmp.DeepEqual(exec.GetMeta().GetArgs()[3:6], []string{"--" + noFollowFlagName + "=true", "/bin/tool", fmt.Sprintf("%o", checker.Permissions.Perm())}))
	})

	t.Run("skip when permissions unset", func(t *testing.T) {
		execs := execsFromDefinition(t, definitionFromStateOption(t, checkFilePerms.WithCheck("/bin/tool", fileCheckFromYAML(t, "{}"), withTestFrontend())))
		assert.Check(t, cmp.Len(execs, 0))
	})
}
