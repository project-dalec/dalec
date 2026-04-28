package testrunner

import (
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestCheckFileEndsWithCommand(t *testing.T) {
	file := filepath.Join(t.TempDir(), "log.txt")
	assert.NilError(t, os.WriteFile(file, []byte("some data trailer"), 0o600))

	t.Run("matches suffix", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileEndsWith.Cmd, file, "trailer")
		assert.Check(t, cmp.Equal(code, 0))
		assert.Check(t, cmp.Equal(stderr, ""))
	})

	t.Run("suffix mismatch", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileEndsWith.Cmd, file, "wrong")
		assert.Check(t, cmp.Equal(code, 3))
		assert.Check(t, cmp.Contains(stderr, "expected: \"wrong\""))
	})
}

func TestCheckFileEndsWithWithCheckLLB(t *testing.T) {
	t.Run("runs when suffix provided", func(t *testing.T) {
		checker := checkOutputFromYAML(t, "ends_with: done\n")
		exec := singleExecOp(t, definitionFromStateOption(t, checkFileEndsWith.WithCheck("/tmp/out", checker, withTestFrontend())))
		expect := []string{frontendMountPath, testRunnerCmdName, string(checkFileEndsWith), "/tmp/out", "done"}
		assert.Check(t, cmp.DeepEqual(expect, exec.GetMeta().GetArgs()))
	})

	t.Run("skip when suffix empty", func(t *testing.T) {
		execs := execsFromDefinition(t, definitionFromStateOption(t, checkFileEndsWith.WithCheck("/tmp/out", checkOutputFromYAML(t, "{}"), withTestFrontend())))
		assert.Check(t, cmp.Len(execs, 0))
	})
}
