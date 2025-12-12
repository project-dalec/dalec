package testrunner

import (
	"fmt"
	"os"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
)

const checkFileEmpty = checkFileEmptyCommand("check-file-empty")

type checkFileEmptyCommand string

func (c checkFileEmptyCommand) WithCheck(p string, checker *dalec.CheckOutput, opts ...ValidationOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		if !checker.Empty {
			return in
		}

		args := []string{
			string(c),
			p,
		}
		opts = append(opts, c.location(in, checker))
		return in.With(doValidate(args, opts...))
	}
}

func (c checkFileEmptyCommand) Kind() string {
	return dalec.CheckOutputEmptyKind
}

func (c checkFileEmptyCommand) location(st llb.State, checker *dalec.CheckOutput) ValidationOpt {
	return func(info *ValidationInfo) {
		info.Constraints = append(info.Constraints, checker.GetSourceLocation(st, c.Kind(), 0))
	}
}

func (c checkFileEmptyCommand) Cmd(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "expected 1 argument: <file-path>")
		exit(1)
	}

	p := args[0]
	stat, err := os.Stat(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		exit(2)
	}

	if stat.Size() == 0 {
		return
	}

	var actual string

	f, err := mmapFile(p)
	if err != nil {
		actual = fmt.Sprintf("<could not read file contents: %v>", err)
	} else {
		actual = previewString(f.Bytes())
	}

	err = &dalec.CheckOutputError{
		Path:   filterPath(p),
		Kind:   c.Kind(),
		Actual: actual,
	}

	fmt.Fprintln(os.Stderr, err)
	exit(3)
}
