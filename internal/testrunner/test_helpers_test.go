package testrunner

import (
	"io"
	"os"
	"testing"
)

type exitPanic struct {
	code int
}

// runCommand executes a command function while capturing its exit code and stderr output.
// Commands invoke exit() directly, so tests override it to panic with the exit code instead.
func runCommand(t *testing.T, cmd func([]string), args ...string) (int, string) {
	t.Helper()

	oldExit := exit
	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating stderr pipe: %v", err)
	}
	exit = func(code int) { panic(exitPanic{code: code}) }
	os.Stderr = w

	var recovered interface{}
	func() {
		defer func() { recovered = recover() }()
		cmd(args)
	}()

	if err := w.Close(); err != nil {
		// Close errors would interfere with the capture and should fail the test.
		t.Fatalf("closing stderr writer: %v", err)
	}
	stderr, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading stderr output: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("closing stderr reader: %v", err)
	}
	os.Stderr = oldStderr
	exit = oldExit

	if recovered != nil {
		if ex, ok := recovered.(exitPanic); ok {
			return ex.code, string(stderr)
		}
		panic(recovered)
	}

	return 0, string(stderr)
}
