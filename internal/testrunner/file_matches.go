package testrunner

import (
	"fmt"
	"os"
	"regexp"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
)

const checkFileMatches = checkFileMatchesCommand("check-file-matches")

type checkFileMatchesCommand string

func (c checkFileMatchesCommand) WithCheck(p string, checker *dalec.CheckOutput, opts ...ValidationOpt) []llb.StateOption {
	if len(checker.Matches) == 0 {
		return nil
	}

	outs := make([]llb.StateOption, 0, len(checker.Matches))
	for i := range checker.Matches {
		outs = append(outs, c.validate(p, checker, i, opts...))
	}

	return outs
}

func (c checkFileMatchesCommand) validate(p string, checker *dalec.CheckOutput, index int, opts ...ValidationOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		args := []string{
			string(c),
			p,
			checker.Matches[index],
		}

		opts := append(opts, c.location(in, checker, index))
		return in.With(doValidate(args, opts...))
	}
}

func (c checkFileMatchesCommand) Kind() string {
	return dalec.CheckOutputMatchesKind
}

func (c checkFileMatchesCommand) location(st llb.State, checker *dalec.CheckOutput, index int) ValidationOpt {
	return func(info *ValidationInfo) {
		info.Constraints = append(info.Constraints, checker.GetSourceLocation(st, c.Kind(), index))
	}
}

func (c checkFileMatchesCommand) Cmd(args []string) {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "expected 2 arguments: <file-path> <regexp pattern>")
		fmt.Fprintln(os.Stderr, "args:", args)
		exit(1)
	}

	pattern := args[1]
	re, err := regexp.Compile(pattern)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error compiling regex pattern", pattern, err)
		exit(2)
	}

	p := args[0]
	f, err := mmapFile(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error opening file:", err)
		exit(2)
	}
	defer f.Close()

	dt := f.Bytes()
	if re.Match(dt) {
		return
	}

	err = &dalec.CheckOutputError{
		Path:     filterPath(p),
		Kind:     dalec.CheckOutputMatchesKind,
		Expected: pattern,
		Actual:   previewString(dt),
	}
	fmt.Fprintln(os.Stderr, err)
	exit(3)
}
