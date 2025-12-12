package testrunner

import (
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestCheckFileContainsCommand(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "log.txt")
	assert.NilError(t, os.WriteFile(file, []byte("hello world"), 0o600))

	t.Run("contains", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileContains.Cmd, file, "world")
		assert.Check(t, cmp.Equal(code, 0))
		assert.Check(t, cmp.Equal(stderr, ""))
	})

	t.Run("missing substring", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileContains.Cmd, file, "mars")
		assert.Check(t, cmp.Equal(code, 3))
		assert.Check(t, cmp.Contains(stderr, "expected: \"mars\""))
	})

	t.Run("invalid args", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileContains.Cmd, file)
		assert.Check(t, cmp.Equal(code, 1))
		assert.Check(t, cmp.Contains(stderr, "expected 2 arguments"))
	})
}

func TestCheckFileContainsWithCheckLLB(t *testing.T) {
	checker := checkOutputFromYAML(t, "contains:\n  - alpha\n  - beta\n")
	stateOpts := checkFileContains.WithCheck("/tmp/output", checker, withTestFrontend())
	assert.Check(t, cmp.Len(stateOpts, len(checker.Contains)))

	for i, opt := range stateOpts {
		def := definitionFromStateOption(t, opt)
		exec := singleExecOp(t, def)
		expect := []string{
			frontendMountPath,
			testRunnerCmdName,
			string(checkFileContains),
			"/tmp/output",
			checker.Contains[i],
		}
		assert.Check(t, cmp.DeepEqual(expect, exec.GetMeta().GetArgs()))
		requireMountDest(t, exec.GetMounts(), frontendMountPath)
		requireMountDest(t, exec.GetMounts(), internalStateMountPath)
	}
}

func TestCheckFileContainsWithCheckLLBSkip(t *testing.T) {
	opts := checkFileContains.WithCheck("/tmp/output", checkOutputFromYAML(t, "{}"), withTestFrontend())
	assert.Check(t, opts == nil)
}
