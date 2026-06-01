package testrunner

import (
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestCheckFileEqualsCommand(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.txt")
	assert.NilError(t, os.WriteFile(file, []byte("exact-value"), 0o600))

	t.Run("exact match", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileEquals.Cmd, file, "exact-value")
		assert.Check(t, cmp.Equal(code, 0))
		assert.Check(t, cmp.Equal(stderr, ""))
	})

	t.Run("not equal", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileEquals.Cmd, file, "other")
		assert.Check(t, cmp.Equal(code, 3))
		assert.Check(t, cmp.Contains(stderr, "expected: \"other\""))
	})
}

func TestCheckFileEqualsWithCheckLLB(t *testing.T) {
	t.Run("executes when equals set", func(t *testing.T) {
		checker := checkOutputFromYAML(t, "equals: data\n")
		exec := singleExecOp(t, definitionFromStateOption(t, checkFileEquals.WithCheck("/tmp/file", checker, withTestFrontend())))
		expect := []string{frontendMountPath, testRunnerCmdName, string(checkFileEquals), "/tmp/file", "data"}
		assert.Check(t, cmp.DeepEqual(expect, exec.GetMeta().GetArgs()))
	})

	t.Run("skip when equals empty", func(t *testing.T) {
		checker := checkOutputFromYAML(t, "{}")
		execs := execsFromDefinition(t, definitionFromStateOption(t, checkFileEquals.WithCheck("/tmp/file", checker, withTestFrontend())))
		assert.Check(t, cmp.Len(execs, 0))
	})
}
