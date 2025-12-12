package testrunner

import (
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
)

const (
	checkFileIsDir = checkFileIsDirCommand("check-file-isdir")
)

type checkFileIsDirCommand string

func (c checkFileIsDirCommand) WithCheck(p string, checker *dalec.FileCheckOutput, opts ...ValidationOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		if checker.NotExist {
			return in
		}

		args := []string{
			string(c),
			"--not=" + strconv.FormatBool(!checker.IsDir),
			"--" + noFollowFlagName + "=" + strconv.FormatBool(checker.NoFollow),
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
	flags := flag.NewFlagSet(string(c), flag.ExitOnError)
	notFl := flags.Bool("not", false, "invert the is_dir check")
	noFollowFl := flags.Bool(noFollowFlagName, false, "do not follow symlinks")
	flags.Parse(args) //nolint:errcheck // already handled by ExitOnError

	if flags.NArg() != 1 {
		flags.Usage()
		fmt.Fprintln(os.Stderr, "error: exactly one path argument is required")
		fmt.Fprintln(os.Stderr, "args:", args)
		exit(1)
	}

	p := flags.Arg(0)
	fi := mustFileInfo(p, *noFollowFl)

	expectDir := !*notFl
	actual := fi.IsDir()

	if actual == expectDir {
		return
	}

	err := &dalec.CheckOutputError{
		Kind:     c.Kind(),
		Path:     p,
		Expected: "is_dir=" + strconv.FormatBool(expectDir),
		Actual:   "is_dir=" + strconv.FormatBool(actual),
	}
	fmt.Fprintln(os.Stderr, err)
	exit(2)
}
