package testrunner

import (
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestCheckFileEmptyCommand(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.txt")
	assert.NilError(t, os.WriteFile(empty, nil, 0o600))
	full := filepath.Join(dir, "full.txt")
	assert.NilError(t, os.WriteFile(full, []byte("data"), 0o600))

	t.Run("empty file", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileEmpty.Cmd, empty)
		assert.Check(t, cmp.Equal(code, 0))
		assert.Check(t, cmp.Equal(stderr, ""))
	})

	t.Run("non-empty", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileEmpty.Cmd, full)
		assert.Check(t, cmp.Equal(code, 3))
		assert.Check(t, cmp.Contains(stderr, "got \"data\""))
	})
}

func TestCheckFileEmptyWithCheckLLB(t *testing.T) {
	t.Run("empty true", func(t *testing.T) {
		exec := singleExecOp(t, definitionFromStateOption(t, checkFileEmpty.WithCheck("/tmp/out", checkOutputFromYAML(t, "empty: true\n"), withTestFrontend())))
		expect := []string{frontendMountPath, testRunnerCmdName, string(checkFileEmpty), "/tmp/out"}
		assert.Check(t, cmp.DeepEqual(expect, exec.GetMeta().GetArgs()))
	})

	t.Run("skip when empty false", func(t *testing.T) {
		execs := execsFromDefinition(t, definitionFromStateOption(t, checkFileEmpty.WithCheck("/tmp/out", checkOutputFromYAML(t, "{}"), withTestFrontend())))
		assert.Check(t, cmp.Len(execs, 0))
	})
}
