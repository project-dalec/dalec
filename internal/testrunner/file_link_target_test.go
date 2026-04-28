package testrunner

import (
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestCheckFileLinkTargetCommand(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	assert.NilError(t, os.WriteFile(target, []byte("data"), 0o600))
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	t.Run("matches target", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileLinkTarget.Cmd, link, target)
		assert.Check(t, cmp.Equal(code, 0))
		assert.Check(t, cmp.Equal(stderr, ""))
	})

	t.Run("unexpected target", func(t *testing.T) {
		const want = "other"
		code, stderr := runCommand(t, checkFileLinkTarget.Cmd, link, want)
		assert.Check(t, cmp.Equal(code, 2))
		assert.Check(t, cmp.Contains(stderr, "expected: \""+want+"\""))
		assert.Check(t, cmp.Contains(stderr, "got \""+target+"\""))
	})

	t.Run("invalid args", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileLinkTarget.Cmd, link)
		assert.Check(t, cmp.Equal(code, 1))
		assert.Check(t, cmp.Contains(stderr, "expected 2 arguments"))
	})
}

func TestCheckFileLinkTargetWithCheckLLB(t *testing.T) {
	t.Run("runs when link target set", func(t *testing.T) {
		checker := fileCheckFromYAML(t, "link_target: /usr/bin/tool\n")
		exec := singleExecOp(t, definitionFromStateOption(t, checkFileLinkTarget.WithCheck("/tmp/link", checker, withTestFrontend())))
		expect := []string{
			frontendMountPath,
			testRunnerCmdName,
			string(checkFileLinkTarget),
			"/tmp/link",
			"/usr/bin/tool",
		}
		assert.Check(t, cmp.DeepEqual(expect, exec.GetMeta().GetArgs()))
	})

	t.Run("skip when link target empty", func(t *testing.T) {
		execs := execsFromDefinition(t, definitionFromStateOption(t, checkFileLinkTarget.WithCheck("/tmp/link", fileCheckFromYAML(t, "{}"), withTestFrontend())))
		assert.Check(t, cmp.Len(execs, 0))
	})
}
