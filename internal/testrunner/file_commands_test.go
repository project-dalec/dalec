package testrunner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckFileContainsCommand(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "log.txt")
	if err := os.WriteFile(file, []byte("hello world"), 0o600); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	t.Run("contains", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileContains.Cmd, file, "world")
		if code != 0 {
			t.Fatalf("expected exit 0, got %d", code)
		}
		if stderr != "" {
			t.Fatalf("expected no stderr, got %q", stderr)
		}
	})

	t.Run("missing substring", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileContains.Cmd, file, "mars")
		if code != 3 {
			t.Fatalf("expected exit 3, got %d", code)
		}
		if !strings.Contains(stderr, "expected: \"mars\"") {
			t.Fatalf("unexpected stderr: %q", stderr)
		}
	})

	t.Run("invalid args", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileContains.Cmd, file)
		if code != 1 {
			t.Fatalf("expected exit 1, got %d", code)
		}
		if !strings.Contains(stderr, "expected 2 arguments") {
			t.Fatalf("unexpected stderr: %q", stderr)
		}
	})
}

func TestCheckFileStartsWithCommand(t *testing.T) {
	file := filepath.Join(t.TempDir(), "log.txt")
	if err := os.WriteFile(file, []byte("prefix-data"), 0o600); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	t.Run("matches prefix", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileStartsWith.Cmd, file, "prefix")
		if code != 0 || stderr != "" {
			t.Fatalf("unexpected result: code=%d stderr=%q", code, stderr)
		}
	})

	t.Run("prefix mismatch", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileStartsWith.Cmd, file, "wrong")
		if code != 3 {
			t.Fatalf("expected exit 3, got %d", code)
		}
		if !strings.Contains(stderr, "expected: \"wrong\"") {
			t.Fatalf("unexpected stderr: %q", stderr)
		}
	})
}

func TestCheckFileEndsWithCommand(t *testing.T) {
	file := filepath.Join(t.TempDir(), "log.txt")
	if err := os.WriteFile(file, []byte("some data trailer"), 0o600); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	t.Run("matches suffix", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileEndsWith.Cmd, file, "trailer")
		if code != 0 || stderr != "" {
			t.Fatalf("unexpected result: code=%d stderr=%q", code, stderr)
		}
	})

	t.Run("suffix mismatch", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileEndsWith.Cmd, file, "wrong")
		if code != 3 {
			t.Fatalf("expected exit 3, got %d", code)
		}
		if !strings.Contains(stderr, "expected: \"wrong\"") {
			t.Fatalf("unexpected stderr: %q", stderr)
		}
	})
}

func TestCheckFileEqualsCommand(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.txt")
	if err := os.WriteFile(file, []byte("exact-value"), 0o600); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	t.Run("exact match", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileEquals.Cmd, file, "exact-value")
		if code != 0 || stderr != "" {
			t.Fatalf("unexpected result: code=%d stderr=%q", code, stderr)
		}
	})

	t.Run("not equal", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileEquals.Cmd, file, "other")
		if code != 3 {
			t.Fatalf("expected exit 3, got %d", code)
		}
		if !strings.Contains(stderr, "expected: \"other\"") {
			t.Fatalf("unexpected stderr: %q", stderr)
		}
	})
}

func TestCheckFileMatchesCommand(t *testing.T) {
	file := filepath.Join(t.TempDir(), "log.txt")
	if err := os.WriteFile(file, []byte("value=42"), 0o600); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	t.Run("regex matches", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileMatches.Cmd, file, `value=\d+`)
		if code != 0 || stderr != "" {
			t.Fatalf("unexpected result: code=%d stderr=%q", code, stderr)
		}
	})

	t.Run("regex mismatch", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileMatches.Cmd, file, `value=\d{3}`)
		if code != 3 {
			t.Fatalf("expected exit 3, got %d", code)
		}
		if !strings.Contains(stderr, "expected: \"value=\\\\d{3}\"") {
			t.Fatalf("unexpected stderr: %q", stderr)
		}
	})

	t.Run("invalid regex", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileMatches.Cmd, file, `*invalid`)
		if code != 2 {
			t.Fatalf("expected exit 2, got %d", code)
		}
		if !strings.Contains(stderr, "error compiling regex pattern") {
			t.Fatalf("unexpected stderr: %q", stderr)
		}
	})
}

func TestCheckFileEmptyCommand(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatalf("creating empty file: %v", err)
	}
	full := filepath.Join(dir, "full.txt")
	if err := os.WriteFile(full, []byte("data"), 0o600); err != nil {
		t.Fatalf("writing full file: %v", err)
	}

	t.Run("empty file", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileEmpty.Cmd, empty)
		if code != 0 || stderr != "" {
			t.Fatalf("unexpected result: code=%d stderr=%q", code, stderr)
		}
	})

	t.Run("non-empty", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileEmpty.Cmd, full)
		if code != 3 {
			t.Fatalf("expected exit 3, got %d", code)
		}
		if !strings.Contains(stderr, "got \"data\"") {
			t.Fatalf("unexpected stderr: %q", stderr)
		}
	})
}

func TestCheckFileExistsCommand(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(file, []byte("ok"), 0o600); err != nil {
		t.Fatalf("writing test file: %v", err)
	}
	missing := filepath.Join(dir, "missing.txt")

	t.Run("file exists", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileExists.Cmd, file)
		if code != 0 || stderr != "" {
			t.Fatalf("unexpected result: code=%d stderr=%q", code, stderr)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileExists.Cmd, missing)
		if code != 2 {
			t.Fatalf("expected exit 2, got %d", code)
		}
		if !strings.Contains(stderr, "expected: \"exists=true\"") {
			t.Fatalf("unexpected stderr: %q", stderr)
		}
	})

	t.Run("expect not exists", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileExists.Cmd, "--not=true", missing)
		if code != 0 || stderr != "" {
			t.Fatalf("unexpected result: code=%d stderr=%q", code, stderr)
		}
	})

	t.Run("no follow symlink", func(t *testing.T) {
		target := filepath.Join(dir, "target.txt")
		symlink := filepath.Join(dir, "link")
		if err := os.Symlink(target, symlink); err != nil {
			t.Skipf("symlink not supported: %v", err)
		}

		code, _ := runCommand(t, checkFileExists.Cmd, symlink)
		if code != 2 {
			t.Fatalf("expected exit 2 when following broken symlink, got %d", code)
		}

		code, stderr := runCommand(t, checkFileExists.Cmd, "--no-follow-symlinks=true", symlink)
		if code != 0 || stderr != "" {
			t.Fatalf("unexpected result without following symlink: code=%d stderr=%q", code, stderr)
		}
	})
}

func TestCheckFileIsDirCommand(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(file, []byte("data"), 0o600); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	t.Run("is directory", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileIsDir.Cmd, dir)
		if code != 0 || stderr != "" {
			t.Fatalf("unexpected result: code=%d stderr=%q", code, stderr)
		}
	})

	t.Run("not directory", func(t *testing.T) {
		code, stderr := runCommand(t, checkFileIsDir.Cmd, file)
		if code != 2 {
			t.Fatalf("expected exit 2, got %d", code)
		}
		if !strings.Contains(stderr, "expected: \"is_dir=true\"") {
			t.Fatalf("unexpected stderr: %q", stderr)
		}
	})
}

func TestCheckFilePermsCommand(t *testing.T) {
	file := filepath.Join(t.TempDir(), "script.sh")
	if err := os.WriteFile(file, []byte("#!/bin/true"), 0o700); err != nil {
		t.Fatalf("writing test file: %v", err)
	}
	if err := os.Chmod(file, 0o764); err != nil {
		t.Fatalf("chmod test file: %v", err)
	}

	t.Run("expected perms", func(t *testing.T) {
		code, stderr := runCommand(t, checkFilePerms.Cmd, file, "764")
		if code != 0 || stderr != "" {
			t.Fatalf("unexpected result: code=%d stderr=%q", code, stderr)
		}
	})

	t.Run("wrong perms", func(t *testing.T) {
		code, stderr := runCommand(t, checkFilePerms.Cmd, file, "644")
		if code != 2 {
			t.Fatalf("expected exit 2, got %d", code)
		}
		if !strings.Contains(stderr, "expected: \"-rw-r--r--\"") {
			t.Fatalf("unexpected stderr: %q", stderr)
		}
	})
}
