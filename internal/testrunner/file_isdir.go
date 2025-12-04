package testrunner

import (
	"fmt"
	"os"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
)

const (
	checkFileIsDir = checkFileIsDirCommand("check-file-isdir")
)

type checkFileIsDirCommand string

func (c checkFileIsDirCommand) Validate(p string, checker *dalec.FileCheckOutput, opts ...ValidationOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		if !checker.IsDir {
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

func (c checkFileIsDirCommand) Kind() string {
	return dalec.CheckFileIsDirKind
}

func (c checkFileIsDirCommand) location(st llb.State, checker *dalec.FileCheckOutput) ValidationOpt {
	return func(info *ValidationInfo) {
		info.Constraints = append(info.Constraints, checker.GetSourceLocation(st, c.Kind(), 0))
	}
}

func (c checkFileIsDirCommand) Cmd(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: "+string(c)+" <file>")
		os.Exit(1)
	}

	p := args[0]
	fi, err := fileInfo(p, false)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error checking file existence:", err)
		os.Exit(2)
	}

	if fi.IsDir() {
		return
	}

	err = &dalec.CheckOutputError{
		Kind:     c.Kind(),
		Path:     p,
		Expected: "is_dir=true",
		Actual:   "is_dir=false",
	}
	fmt.Fprintln(os.Stderr, err)
	os.Exit(2)
}
