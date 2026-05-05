package testrunner

import (
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestCheckFileIsDirCommand(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file.txt")
	assert.NilError(t, os.WriteFile(file, []byte("data"), 0o600))

	t.Run("is directory", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileIsDir.Cmd, dir)
		assert.Check(t, cmp.Equal(code, 0))
		assert.Check(t, cmp.Equal(stderr, ""))
	})

	t.Run("not directory", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileIsDir.Cmd, file)
		assert.Check(t, cmp.Equal(code, 2))
		assert.Check(t, cmp.Contains(stderr, "expected: \"is_dir=true\""))
	})

	t.Run("symlink respects follow flag", func(t *testing.T) {
		dir := t.TempDir()
		expectDir := filepath.Join(dir, "dir")
		assert.NilError(t, os.Mkdir(expectDir, 0o755))
		link := filepath.Join(dir, "link")
		if err := os.Symlink(expectDir, link); err != nil {
			t.Skip("symlinks unsupported:", err)
		}

		code, stderr := runCommand(t, checkFileIsDir.Cmd, link)
		assert.Check(t, cmp.Equal(code, 0))
		assert.Check(t, cmp.Equal(stderr, ""))

		code, stderr = runCommand(t, checkFileIsDir.Cmd, "--"+noFollowFlagName+"=true", link)
		assert.Check(t, cmp.Equal(code, 2))
		assert.Check(t, cmp.Contains(stderr, "expected: \"is_dir=true\""))
	})
}

func TestCheckFileIsDirWithCheckLLB(t *testing.T) {
	t.Run("is dir true", func(t *testing.T) {
		checker := fileCheckFromYAML(t, "is_dir: true\n")
		exec := singleExecOp(t, definitionFromStateOption(t, checkFileIsDir.WithCheck("/data", checker, withTestFrontend())))
		expect := []string{
			frontendMountPath,
			testRunnerCmdName,
			string(checkFileIsDir),
			"--not=false",
			"--" + noFollowFlagName + "=false",
			"/data",
		}
		assert.Check(t, cmp.DeepEqual(expect, exec.GetMeta().GetArgs()))
	})

	t.Run("not dir expectation by default", func(t *testing.T) {
		exec := singleExecOp(t, definitionFromStateOption(t, checkFileIsDir.WithCheck("/file", fileCheckFromYAML(t, "{}"), withTestFrontend())))
		expect := []string{
			frontendMountPath,
			testRunnerCmdName,
			string(checkFileIsDir),
			"--not=true",
			"--" + noFollowFlagName + "=false",
			"/file",
		}
		assert.Check(t, cmp.DeepEqual(expect, exec.GetMeta().GetArgs()))
	})

	t.Run("no follow flag", func(t *testing.T) {
		checker := fileCheckFromYAML(t, "is_dir: true\nno_follow: true\n")
		exec := singleExecOp(t, definitionFromStateOption(t, checkFileIsDir.WithCheck("/data", checker, withTestFrontend())))
		assert.Check(t, cmp.DeepEqual(exec.GetMeta().GetArgs()[3:6], []string{"--not=false", "--" + noFollowFlagName + "=true", "/data"}))
	})
}
