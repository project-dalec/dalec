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

const checkFilePerms = checkFilePermsCommand("check-file-perms")

type checkFilePermsCommand string

func (c checkFilePermsCommand) Validate(p string, checker *dalec.FileCheckOutput, opts ...ValidationOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		if checker.Permissions == 0 {
			return in
		}

		args := []string{
			string(c),
			p,
			fmt.Sprintf("%o", checker.Permissions.Perm()),
		}

		opts = append(opts, c.location(in, checker))
		return in.With(doValidate(args, opts...))
	}
}

func (c checkFilePermsCommand) Kind() string {
	return dalec.CheckFilePermissionsKind
}

func (c checkFilePermsCommand) location(st llb.State, checker *dalec.FileCheckOutput) ValidationOpt {
	return func(info *ValidationInfo) {
		info.Constraints = append(info.Constraints, checker.GetSourceLocation(st, c.Kind(), 0))
	}
}

func (c checkFilePermsCommand) Cmd(args []string) {
	flags := flag.NewFlagSet(string(c), flag.ExitOnError)
	noFollowFl := flags.Bool("no-follow-symlinks", false, "do not follow symlinks")

	flags.Parse(args) //nolint:errcheck // errors are handled by ExitOnError

	if flag.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "expected 2 arguments: <file-path> <permissions in octal>")
		fmt.Fprintln(os.Stderr, "args:", args)
		os.Exit(1)
		return
	}

	p := flags.Arg(0)
	perms, err := strconv.ParseUint(flags.Arg(1), 8, 32)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error parsing perms:", err)
		os.Exit(1)
	}

	fi, err := fileInfo(p, *noFollowFl)
	if err != nil {
		os.Exit(1)
	}

	expected := fs.FileMode(perms)
	actual := fi.Mode().Perm()

	if actual == expected {
		return
	}

	err = &dalec.CheckOutputError{
		Path:     p,
		Kind:     c.Kind(),
		Expected: expected.String(),
		Actual:   actual.String(),
	}
	fmt.Fprintln(os.Stderr, err)
	os.Exit(2)
}
