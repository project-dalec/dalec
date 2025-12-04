package testrunner

import (
	"bytes"
	"fmt"
	"os"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
)

const checkFileContains = checkFileContainsCommand("check-file-contains")

type checkFileContainsCommand string

func (c checkFileContainsCommand) Validate(p string, checker *dalec.CheckOutput, opts ...ValidationOpt) []llb.StateOption {
	if len(checker.Contains) == 0 {
		return nil
	}

	outs := make([]llb.StateOption, 0, len(checker.Contains))
	for i := range checker.Contains {
		outs = append(outs, c.validate(p, checker, i, opts...))
	}
	return outs
}

func (c checkFileContainsCommand) validate(p string, checker *dalec.CheckOutput, index int, opts ...ValidationOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		args := []string{
			string(c),
			p,
			checker.Contains[index],
		}
		opts := append(opts, c.location(in, checker, index))
		return in.With(doValidate(args, opts...))
	}
}

func (c checkFileContainsCommand) Kind() string {
	return dalec.CheckOutputContainsKind
}

func (c checkFileContainsCommand) location(st llb.State, checker *dalec.CheckOutput, index int) ValidationOpt {
	return func(info *ValidationInfo) {
		info.Constraints = append(info.Constraints, checker.GetSourceLocation(st, c.Kind(), index))
	}
}

func (c checkFileContainsCommand) Cmd(args []string) {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "expected 2 arguments: <file-path> <contains-string>")
		os.Exit(1)
	}

	p := args[0]
	f, err := mmapFile(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error opening file:", err)
		os.Exit(2)
	}
	defer f.Close()

	expect := args[1]
	dt := f.Bytes()
	if bytes.Contains(dt, []byte(expect)) {
		return
	}

	err = &dalec.CheckOutputError{
		Path:     filterPath(p),
		Kind:     c.Kind(),
		Expected: expect,
		Actual:   previewString(dt),
	}

	fmt.Fprintln(os.Stderr, err)
	os.Exit(3)
}
