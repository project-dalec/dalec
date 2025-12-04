package frontend

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"strconv"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/pkg/errors"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/internal/testrunner"
)

// RunTests runs the tests defined in the spec against the given target container.
func RunTests(ctx context.Context, client gwclient.Client, sOpt dalec.SourceOpts, spec *dalec.Spec, withTestDeps llb.StateOption, target string, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		if skipVar := client.BuildOpts().Opts["build-arg:"+"DALEC_SKIP_TESTS"]; skipVar != "" {
			skip, err := strconv.ParseBool(skipVar)
			if err != nil {
				return dalec.ErrorState(in, errors.Wrapf(err, "could not parse build-arg %s", "DALEC_SKIP_TESTS"))
			}
			if skip {
				Warn(ctx, client, llb.Scratch(), "Tests skipped due to build-arg DALEC_SKIP_TESTS="+skipVar)
				return in
			}
		}

		tests := spec.Tests

		t, ok := spec.Targets[target]
		if ok {
			tests = append(tests, t.Tests...)
		}

		if len(tests) == 0 {
			return in
		}

		frontendSt := GetCurrentFrontend(client)
		runTests := testrunner.WithTests(target, frontendSt, sOpt, withTestDeps, tests, opts...)
		return in.With(runTests).With(testrunner.WithFinalState(frontendSt, in))
	}
}

type ValidationOpt func(*ValidationInfo)

type ValidationInfo struct {
	Frontend    *llb.State
	Constraints []llb.ConstraintsOpt
}

func ValidateWithConstraints(opts ...llb.ConstraintsOpt) ValidationOpt {
	return func(vi *ValidationInfo) {
		vi.Constraints = append(vi.Constraints, opts...)
	}
}

func WithClientFrontend(client gwclient.Client) ValidationOpt {
	return func(vi *ValidationInfo) {
		st := getCurrentFrontend(client)
		vi.Frontend = st
	}
}

type testRunner struct {
	frontend llb.State
}

func (tr *testRunner) Run(ctx context.Context, args []string) {
	flags := flag.NewFlagSet("test-runner", flag.ExitOnError)
	flags.Parse(args)

	if flags.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "no test-runner command provided")
		os.Exit(1)
	}

	cmd := flags.Arg(0)
	switch cmd {
	case string(checkFilePerms):
		checkFilePerms.Cmd(args)
	case string(checkFileExists):
		checkFileExists.Cmd(args)
	default:
		fmt.Fprintln(os.Stderr, "Unknown test-runner command:", cmd)
		fmt.Fprintln(os.Stderr, "If you see this error it is a bug")
		os.Exit(70) // 70 is EX_SOFTWARE, meaning internal software error occurred
	}
}

const (
	checkFilePerms  = checkFilePermsCommand("check-file-perms")
	checkFileExists = checkFileExistsCommand("check-file-exists")
	checkFileContains = checkFileContains("check-file-contains")
)

type checkFileContains string

func(c checkFileContains) Validate(p string, contains string, opts ...ValidationOpt) llb.StateOption {
	args := []string{
		string(c),
		contains,
	}
	return doValidate(args, opts...)
}

func(c checkFileContains) Cmd(args []string) {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "expected 2 arguments: <file-path> <contains-string>")
		os.Exit(1)
	}
}

type checkFileExistsCommand string

func (c checkFileExistsCommand) Validate(p string, checker dalec.FileCheckOutput, opts ...ValidationOpt) llb.StateOption {
	args := []string{
		string(c),
		"--not="+strconv.FormatBool(checker.NotExist),
	}
	return doValidate(args, opts...)
}

func noFollowSymlinksFlag(flags *flag.FlagSet) *bool {
	return flags.Bool("no-follow-symlinks", defaultNoFollowSymlinks, "do not follow symlinks")
}

func (c checkFileExistsCommand) Cmd(args []string) {
	flags := flag.NewFlagSet(string(c), flag.ExitOnError)
	notFl := flags.Bool("not", false, "expect the file to not exist")
	noFollowFl := noFollowSymlinksFlag(flags)
	flags.Parse(args) //nolint:errcheck // errors are handled by ExitOnError

	if flag.NArg() != 1 {
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
		Path:     p,
		Expected: "exists=" + strconv.FormatBool(!not),
		Actual:   "exists=" + strconv.FormatBool(notExists),
	}
	fmt.Fprintln(os.Stderr, err)
	os.Exit(2)
}

type checkFilePermsCommand string

const defaultNoFollowSymlinks = true

func validationOpts(opts ...ValidationOpt) ValidationInfo {
	var i ValidationInfo
	for _, o := range opts {
		o(&i)
	}

	if i.Frontend == nil {
		panic("missing frontend state in validation opt -- if you see this it is a bug")
	}

	return i
}

func doValidate(args []string, opts ...ValidationOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		const (
			p = "/tmp/internal/dalec/testrunner/frontend"
			internalStatePath = "/tmp/internal/dalec/testrunner/__internal_state"
		)
		info := validationOpts(opts...)

		args = append([]string{p}, args...)

		return in.Run(
			dalec.WithConstraints(info.Constraints...),
			llb.AddMount(p, *info.Frontend, llb.Readonly, llb.SourcePath("/frontend"))
			llb.Args(args),
		).AddMount(testRunnerInternalSatePath, in)
	}
}

func (c checkFilePermsCommand) Validate(p string, checker dalec.FileCheckOutput, opts ...ValidationOpt) llb.StateOption {
	args := []string{
		string(c),
		p, fmt.Sprintf("%o", checker.Permissions.Perm()),
	}
	return doValidate(args, opts...)
}

func fileInfo(p string, noFollow bool) (fs.FileInfo, error) {
	if noFollow {
		return os.Lstat(p)
	}
	return os.Stat(p)
}

func (c checkFilePermsCommand) Cmd(args []string) {
	flags := flag.NewFlagSet(string(c), flag.ExitOnError)
	noFollowFl := flags.Bool("no-follow-symlinks", defaultNoFollowSymlinks, "do not follow symlinks")
	flags.Parse(args) //nolint:errcheck // errors are handled by ExitOnError

	if flag.NArg() != 2 {
		flags.Usage()
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

	if actual != expected {
		err := &dalec.CheckOutputError{
			Kind:     dalec.CheckFilePermissionsKind,
			Expected: expected.String(),
			Actual:   actual.String(),
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}
