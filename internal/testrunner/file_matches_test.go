package testrunner

import (
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestCheckFileMatchesCommand(t *testing.T) {
	file := filepath.Join(t.TempDir(), "log.txt")
	assert.NilError(t, os.WriteFile(file, []byte("value=42"), 0o600))

	t.Run("regex matches", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileMatches.Cmd, file, `value=\d+`)
		assert.Check(t, cmp.Equal(code, 0))
		assert.Check(t, cmp.Equal(stderr, ""))
	})

	t.Run("regex mismatch", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileMatches.Cmd, file, `value=\d{3}`)
		assert.Check(t, cmp.Equal(code, 3))
		assert.Check(t, cmp.Contains(stderr, "expected: \"value=\\\\d{3}\""))
	})

	t.Run("invalid regex", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileMatches.Cmd, file, `*invalid`)
		assert.Check(t, cmp.Equal(code, 2))
		assert.Check(t, cmp.Contains(stderr, "error compiling regex pattern"))
	})
}

func TestCheckFileMatchesWithCheckLLB(t *testing.T) {
	checker := checkOutputFromYAML(t, "matches:\n  - foo\n  - bar\n")
	stateOpts := checkFileMatches.WithCheck("/tmp/log", checker, withTestFrontend())
	assert.Check(t, cmp.Len(stateOpts, 2))

	for i, opt := range stateOpts {
		exec := singleExecOp(t, definitionFromStateOption(t, opt))
		expect := []string{frontendMountPath, testRunnerCmdName, string(checkFileMatches), "/tmp/log", checker.Matches[i]}
		assert.Check(t, cmp.DeepEqual(expect, exec.GetMeta().GetArgs()))
		requireMountDest(t, exec.GetMounts(), frontendMountPath)
		requireMountDest(t, exec.GetMounts(), internalStateMountPath)
	}
}

func TestCheckFileMatchesWithCheckSkip(t *testing.T) {
	opts := checkFileMatches.WithCheck("/tmp/log", checkOutputFromYAML(t, "{}"), withTestFrontend())
	assert.Check(t, opts == nil)
}
