package testrunner

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"strconv"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
)

const (
	checkFileExists = checkFileExistsCommand("check-file-exists")

	noFollowFlagName = "no-follow-symlinks"
)

type checkFileExistsCommand string

func (c checkFileExistsCommand) Validate(p string, checker *dalec.FileCheckOutput, opts ...ValidationOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		args := []string{
			string(c),
			"--not=" + strconv.FormatBool(checker.NotExist),
			setNoFollowFlag(checker),
			p,
		}

		opts = append(opts, c.location(in, checker))
		return in.With(doValidate(args, opts...))
	}
}

func (c checkFileExistsCommand) Kind() string {
	return dalec.CheckFileNotExistsKind
}

func (c checkFileExistsCommand) location(st llb.State, checker *dalec.FileCheckOutput) ValidationOpt {
	return func(info *ValidationInfo) {
		info.Constraints = append(info.Constraints, checker.GetSourceLocation(st, c.Kind(), 0))
	}
}

func setNoFollowFlag(checker *dalec.FileCheckOutput) string {
	return "--" + noFollowFlagName + "=" + strconv.FormatBool(false) // TODO: DNM: THIS NEEDS TO BE UPDATED TO SUPPORT THE NEW OPTION, BUT NEEDS PAINFUL REBASE
}

func noFollowSymlinksFlag(flags *flag.FlagSet) *bool {
	return flags.Bool(noFollowFlagName, false, "do not follow symlinks")
}

func (c checkFileExistsCommand) Cmd(args []string) {
	flags := flag.NewFlagSet(string(c), flag.ExitOnError)
	notFl := flags.Bool("not", false, "expect the file to not exist")
	noFollowFl := noFollowSymlinksFlag(flags)
	flags.Parse(args) //nolint:errcheck // errors are handled by ExitOnError

	if flags.NArg() != 1 {
		flags.Usage()
		os.Exit(1)
	}

	not := *notFl

	p := flags.Arg(0)
	_, err := fileInfo(p, *noFollowFl)
	if err != nil && !os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "error checking file existence:", err)
		os.Exit(2)
	}

	notExists := os.IsNotExist(err)
	if notExists == not {
		return
	}

	err = &dalec.CheckOutputError{
		Kind:     c.Kind(),
		Path:     p,
		Expected: "exists=" + strconv.FormatBool(!not),
		Actual:   "exists=" + strconv.FormatBool(notExists),
	}
	fmt.Fprintln(os.Stderr, err)
	os.Exit(2)
}

func fileInfo(p string, noFollow bool) (fs.FileInfo, error) {
	if noFollow {
		return os.Lstat(p)
	}
	return os.Stat(p)
}
