package testrunner

import (
	"bytes"
	"fmt"
	"os"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
)

const checkFileStartsWith = checkFileStartsWithCommand("check-file-starts-with")

type checkFileStartsWithCommand string

func (c checkFileStartsWithCommand) WithCheck(p string, checker *dalec.CheckOutput, opts ...ValidationOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		args := []string{
			string(c),
			p,
			checker.StartsWith,
		}
		opts = append(opts, c.location(in, checker))
		return in.With(doValidate(args, opts...))
	}
}

func (c checkFileStartsWithCommand) Kind() string {
	return dalec.CheckOutputStartsWithKind
}

func (c checkFileStartsWithCommand) location(st llb.State, checker *dalec.CheckOutput) ValidationOpt {
	return func(info *ValidationInfo) {
		info.Constraints = append(info.Constraints, checker.GetSourceLocation(st, c.Kind(), 0))
	}
}

func (c checkFileStartsWithCommand) Cmd(args []string) {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "expected 2 arguments: <file-path> <starts-with-string>")
		exit(1)
	}

	p := args[0]
	f, err := mmapFile(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		exit(2)
	}
	defer f.Close()

	dt := f.Bytes()
	startsWith := args[1]
	if bytes.HasPrefix(dt, []byte(startsWith)) {
		return
	}

	err = &dalec.CheckOutputError{
		Path:     filterPath(p),
		Kind:     c.Kind(),
		Expected: startsWith,
		Actual:   previewString(dt),
	}
	fmt.Fprintln(os.Stderr, err)
	exit(3)
}
