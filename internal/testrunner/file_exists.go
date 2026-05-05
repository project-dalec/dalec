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
	checkFileNotExists = checkFileNotExistsCommand("check-file-not-exists")

	noFollowFlagName = "no-follow-symlinks"
)

type checkFileNotExistsCommand string

func (c checkFileNotExistsCommand) WithCheck(p string, checker *dalec.FileCheckOutput, opts ...ValidationOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		if !checker.NotExist {
			// This check is only for non-existence.
			// If the file is expected to exist, skip this check.
			return in
		}

		args := []string{
			string(c),
			"--" + noFollowFlagName + "=" + strconv.FormatBool(checker.NoFollow),
			p,
		}

		opts = append(opts, c.location(in, checker))
		return in.With(doValidate(args, opts...))
	}
}

func (c checkFileNotExistsCommand) Kind() string {
	return dalec.CheckFileNotExistsKind
}

func (c checkFileNotExistsCommand) location(st llb.State, checker *dalec.FileCheckOutput) ValidationOpt {
	return func(info *ValidationInfo) {
		info.Constraints = append(info.Constraints, checker.GetSourceLocation(st, c.Kind(), 0))
	}
}

func (c checkFileNotExistsCommand) newNotExistsError(p string, shouldExist, doesExist bool) error {
	return &dalec.CheckOutputError{
		Kind:     c.Kind(),
		Path:     p,
		Expected: "exists=" + strconv.FormatBool(shouldExist),
		Actual:   "exists=" + strconv.FormatBool(doesExist),
	}
}

func (c checkFileNotExistsCommand) Cmd(args []string) {
	flags := flag.NewFlagSet(string(c), flag.ExitOnError)
	noFollowFl := flags.Bool(noFollowFlagName, false, "do not follow symlinks")
	flags.Parse(args) //nolint:errcheck // errors are handled by ExitOnError

	if flags.NArg() != 1 {
		flags.Usage()
		exit(1)
	}

	p := flags.Arg(0)

	_, err := fileInfo(p, *noFollowFl)
	notExist := os.IsNotExist(err)

	if err != nil && !notExist {
		fmt.Fprintln(os.Stderr, "error checking file existence:", err)
		exit(2)
	}

	if notExist {
		return
	}

	err = c.newNotExistsError(p, false, true)
	fmt.Fprintln(os.Stderr, err)
	exit(2)
}

func fileInfo(p string, noFollow bool) (fs.FileInfo, error) {
	if noFollow {
		return os.Lstat(p)
	}
	return os.Stat(p)
}

func mustFileInfo(p string, noFollow bool) fs.FileInfo {
	info, err := fileInfo(p, noFollow)
	if err == nil {
		return info
	}

	if os.IsNotExist(err) {
		err = checkFileNotExists.newNotExistsError(p, true, false)
		fmt.Fprintln(os.Stderr, err)
		exit(2)
	}

	fmt.Fprintln(os.Stderr, "error checking file info:", err)
	exit(2)
	// unreachable
	return info
}
