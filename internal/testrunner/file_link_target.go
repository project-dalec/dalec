package testrunner

import (
	"fmt"
	"os"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
)

const checkFileLinkTarget = checkFileLinkTargetCommand("check-link-target")

type checkFileLinkTargetCommand string

func (c checkFileLinkTargetCommand) WithCheck(p string, checker *dalec.FileCheckOutput, opts ...ValidationOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		if checker.LinkTarget == "" {
			return in
		}

		args := []string{
			string(c),
			p,
			checker.LinkTarget,
		}
		opts = append(opts, c.location(in, checker))
		return in.With(doValidate(args, opts...))
	}
}

func (c checkFileLinkTargetCommand) location(st llb.State, checker *dalec.FileCheckOutput) ValidationOpt {
	return func(info *ValidationInfo) {
		info.Constraints = append(info.Constraints, checker.GetSourceLocation(st, c.Kind(), 0))
	}
}

func (c checkFileLinkTargetCommand) Kind() string {
	return dalec.CheckFileLinkTargetPathKind
}

func (c checkFileLinkTargetCommand) Cmd(args []string) {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "expected 2 arguments: <file-path> <link-target>")
		exit(1)
	}

	p := args[0]
	expected := args[1]

	actual, err := os.Readlink(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		exit(2)
	}

	if expected == actual {
		return
	}

	err = &dalec.CheckOutputError{
		Kind:     c.Kind(),
		Path:     p,
		Expected: expected,
		Actual:   actual,
	}
	fmt.Fprintln(os.Stderr, err)
	exit(2)
}
