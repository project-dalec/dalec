package testrunner

import (
	"bytes"
	"fmt"
	"os"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
)

const checkFileEquals = checkFileEqualsCommand("check-file-equals")

type checkFileEqualsCommand string

func (c checkFileEqualsCommand) Validate(p string, checker *dalec.CheckOutput, opts ...ValidationOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		if checker.Equals == "" {
			return in
		}

		args := []string{
			string(c),
			p,
			checker.Equals,
		}

		opts = append(opts, c.location(in, checker))
		return in.With(doValidate(args, opts...))
	}
}

func (c checkFileEqualsCommand) Kind() string {
	return dalec.CheckOutputEqualsKind
}

func (c checkFileEqualsCommand) location(st llb.State, checker *dalec.CheckOutput) ValidationOpt {
	return func(info *ValidationInfo) {
		info.Constraints = append(info.Constraints, checker.GetSourceLocation(st, c.Kind(), 0))
	}
}

func (c checkFileEqualsCommand) Cmd(args []string) {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "expected 2 arguments: <file-path> <file-equals-content>")
		os.Exit(1)
	}

	p := args[0]
	f, err := mmapFile(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	defer f.Close()

	dt := f.Bytes()
	equals := args[1]
	if bytes.Equal(dt, []byte(equals)) {
		return
	}

	err = &dalec.CheckOutputError{
		Path:     filterPath(p),
		Kind:     c.Kind(),
		Expected: equals,
		Actual:   previewString(dt),
	}
	fmt.Fprintln(os.Stderr, err)
	os.Exit(3)
}
