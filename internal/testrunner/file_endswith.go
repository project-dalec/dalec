package testrunner

import (
	"bytes"
	"fmt"
	"os"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
)

const checkFileEndsWith checkFileEndsWithCommand = "check-file-endswith"

type checkFileEndsWithCommand string

func (c checkFileEndsWithCommand) WithCheck(p string, checker *dalec.CheckOutput, opts ...ValidationOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		if checker.EndsWith == "" {
			return in
		}
		args := []string{
			string(c),
			p,
			checker.EndsWith,
		}

		opts := append(opts, c.location(in, checker))
		return in.With(doValidate(args, opts...))
	}
}

func (c checkFileEndsWithCommand) Kind() string {
	return dalec.CheckOutputEndsWithKind
}

func (c checkFileEndsWithCommand) location(st llb.State, checker *dalec.CheckOutput) ValidationOpt {
	return func(info *ValidationInfo) {
		info.Constraints = append(info.Constraints, checker.GetSourceLocation(st, c.Kind(), 0))
	}
}

func (c checkFileEndsWithCommand) Cmd(args []string) {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "expected 2 arguments: <file-path> <ends-with-string>")
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
	endsWith := args[1]
	if bytes.HasSuffix(dt, []byte(endsWith)) {
		return
	}

	err = &dalec.CheckOutputError{
		Path:     filterPath(p),
		Kind:     c.Kind(),
		Expected: endsWith,
		Actual:   previewString(dt),
	}
	fmt.Fprintln(os.Stderr, err)
	exit(3)
}
