package testrunner

import (
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestCheckFileStartsWithCommand(t *testing.T) {
	file := filepath.Join(t.TempDir(), "log.txt")
	assert.NilError(t, os.WriteFile(file, []byte("prefix-data"), 0o600))

	t.Run("matches prefix", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileStartsWith.Cmd, file, "prefix")
		assert.Check(t, cmp.Equal(code, 0))
		assert.Check(t, cmp.Equal(stderr, ""))
	})

	t.Run("prefix mismatch", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileStartsWith.Cmd, file, "wrong")
		assert.Check(t, cmp.Equal(code, 3))
		assert.Check(t, cmp.Contains(stderr, "expected: \"wrong\""))
	})
}

func TestCheckFileStartsWithWithCheckLLB(t *testing.T) {
	checker := checkOutputFromYAML(t, "starts_with: hello\n")
	exec := singleExecOp(t, definitionFromStateOption(t, checkFileStartsWith.WithCheck("/tmp/out", checker, withTestFrontend())))
	expect := []string{frontendMountPath, testRunnerCmdName, string(checkFileStartsWith), "/tmp/out", "hello"}
	assert.Check(t, cmp.DeepEqual(expect, exec.GetMeta().GetArgs()))
}
