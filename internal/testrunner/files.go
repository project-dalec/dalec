package testrunner

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strconv"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/internal/commands"
	"github.com/project-dalec/dalec/internal/plugins"
	"golang.org/x/sys/unix"
)

func init() {
	commands.RegisterPlugin(fileCheckerCmdName, plugins.CmdHandlerFunc(fileCheckCmd))
}

const fileCheckerCmdName = "test-filechecker"

func fileCheckCmd(ctx context.Context, args []string) {
	flags := flag.NewFlagSet(fileCheckerCmdName, flag.ExitOnError)

	if err := flags.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "error parsing flags:", err)
		os.Exit(1)
	}

	const usage = fileCheckerCmdName + " <check type> <file> [<check value>]"

	if flags.NArg() < 2 {
		var ok bool
		if !ok {
			fmt.Fprintln(os.Stderr, "usage:", usage)
			os.Exit(1)
		}
	}

	if err := doFileCheck(ctx, flags.Arg(0), flags.Arg(1), flags.Arg(2)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func doFileCheck(ctx context.Context, checkType, filePath, checkValue string) error {
	newErr := func(expected, actual string) error {
		return &dalec.CheckOutputError{
			Kind:     checkType,
			Path:     filePath,
			Expected: expected,
			Actual:   actual,
		}
	}

	newInvalidCheckValue := func(err error) error {
		return fmt.Errorf("invalid check value for %s: %s: %w", checkType, checkValue, err)
	}

	switch checkType {
	case dalec.CheckFileIsDirKind:
		v, err := strconv.ParseBool(checkValue)
		if err != nil {
			return newInvalidCheckValue(err)
		}

		stat, err := os.Stat(filePath)
		if err != nil {
			return err
		}

		if v && !stat.IsDir() {
			return newErr("ModeDir", "ModeFile")
		}

		if !v && stat.IsDir() {
			return newErr("ModeFile", "ModeDir")
		}
		return nil
	case dalec.CheckFileNotExistsKind:
		v, err := strconv.ParseBool(checkValue)
		if err != nil {
			return newInvalidCheckValue(err)
		}

		_, err = os.Stat(filePath)

		notExist := os.IsNotExist(err)
		if notExist == v {
			return nil
		}

		if err != nil && !notExist {
			return err
		}

		return newErr("exists="+strconv.FormatBool(!v), "exists="+strconv.FormatBool(!notExist))
	case dalec.CheckFilePermissionsKind:
		v, err := strconv.ParseUint(checkValue, 8, 32)
		if err != nil {
			return newInvalidCheckValue(err)
		}

		expected := os.FileMode(v).Perm()

		stat, err := os.Stat(filePath)
		if err != nil {
			return err
		}

		actual := stat.Mode().Perm()

		if expected == actual {
			return nil
		}
		return newErr(expected.String(), actual.String())
	case dalec.CheckOutputContainsKind:
		buf, err := mmapFile(filePath)
		if err != nil {
			return err
		}
		defer buf.Close()

		if bytes.Contains(buf.Bytes(), []byte(checkValue)) {
			return nil
		}

		return newErr(checkValue, previewString(buf.Bytes()))
	case dalec.CheckOutputMatchesKind:
		buf, err := mmapFile(filePath)
		if err != nil {
			return err
		}
		defer buf.Close()

		re, err := regexp.Compile(checkValue)
		if err != nil {
			return err
		}

		ok := re.Match(buf.Bytes())
		if ok {
			return nil
		}
		return newErr(checkValue, previewString(buf.Bytes()))
	case dalec.CheckOutputStartsWithKind:
		f, err := mmapFile(filePath)
		if err != nil {
			return err
		}
		defer f.Close()

		dt := f.Bytes()
		if bytes.HasPrefix(dt, []byte(checkValue)) {
			return nil
		}
		return newErr(checkValue, previewString(dt))
	case dalec.CheckOutputEndsWithKind:
		f, err := mmapFile(filePath)
		if err != nil {
			return err
		}
		defer f.Close()

		dt := f.Bytes()
		if bytes.HasSuffix(dt, []byte(checkValue)) {
			return nil
		}
		return newErr(checkValue, previewString(dt))
	case dalec.CheckOutputEmptyKind:
		if checkValue != "" {
			return newInvalidCheckValue(fmt.Errorf("expected empty string"))
		}

		f, err := mmapFile(filePath)
		if err != nil {
			return err
		}
		defer f.Close()

		dt := f.Bytes()
		if len(dt) == 0 {
			return nil
		}
		return newErr("empty", previewString(dt))
	default:
		return fmt.Errorf("unknown check type: %s -- if you see this, this is a bug", checkType)
	}
}

func withFileChecks(frontend llb.State, test *dalec.TestSpec, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		if len(test.Files) == 0 {
			return in
		}

		states := make([]llb.State, 0, len(test.Files))
		for file, check := range test.Files {
			st := in.With(withFileCheck(frontend, file, &check, opts...))
			states = append(states, st)
		}

		if len(states) == 0 {
			return in
		}

		return dalec.MergeAtPath(in, states, "/", opts...)
	}
}

func withFileCheck(frontend llb.State, file string, check *dalec.FileCheckOutput, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		var states []llb.State

		isDirCheck := in.Run(
			llb.AddMount(testRunnerPath, frontend, llb.SourcePath("/frontend")),
			llb.Args([]string{testRunnerPath, fileCheckerCmdName, dalec.CheckFileIsDirKind, file, strconv.FormatBool(check.IsDir)}),
			dalec.WithConstraints(opts...),
			check.GetSourceLocation(in, dalec.CheckFileIsDirKind, 0),
		).Root()
		states = append(states, isDirCheck)

		notEistsCheck := in.Run(
			llb.AddMount(testRunnerPath, frontend, llb.SourcePath("/frontend")),
			llb.Args([]string{testRunnerPath, fileCheckerCmdName, dalec.CheckFileNotExistsKind, file, strconv.FormatBool(check.NotExist)}),
			dalec.WithConstraints(opts...),
			check.GetSourceLocation(in, dalec.CheckFileNotExistsKind, 0),
		).Root()
		states = append(states, notEistsCheck)

		if check.Permissions != 0 {
			permcheck := in.Run(
				llb.AddMount(testRunnerPath, frontend, llb.SourcePath("/frontend")),
				llb.Args([]string{testRunnerPath, fileCheckerCmdName, dalec.CheckFilePermissionsKind, file, fmt.Sprintf("%o", check.Permissions)}),
				dalec.WithConstraints(opts...),
				check.GetSourceLocation(in, dalec.CheckFilePermissionsKind, 0),
			).Root()
			states = append(states, permcheck)
		}

		if !check.CheckOutput.IsEmpty() {
			st := in.With(withCheckOutput(frontend, file, &check.CheckOutput, opts...))
			states = append(states, st)
		}

		if len(states) == 0 {
			return in
		}

		return dalec.MergeAtPath(in, states, "/", opts...)
	}
}

// mmapBuffer represents a memory-mapped file.
// It holds the file descriptor and the mapped data.
// This is useful when reading large files for checks without loading the entire file into memory.
// Such is the case for file content checks like "contains" or "matches".
type mmapBuffer struct {
	f  *os.File
	dt []byte
}

func (mf *mmapBuffer) Close() {
	if mf.dt != nil {
		unix.Munmap(mf.dt) //nolint:errcheck
	}
	mf.f.Close() //nolint:errcheck
}

func mmapFile(path string) (*mmapBuffer, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}

	mf := &mmapBuffer{f: f}
	size := stat.Size()
	if size == 0 {
		mf.dt = []byte{}
		return mf, nil
	}

	dt, err := unix.Mmap(int(mf.f.Fd()), 0, int(size), unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		return nil, err
	}

	mf.dt = dt
	return mf, nil
}

// So yeah, this is mmmap data.
// Don't try to write to it or the gates of hell will open and come to devour us all.
func (mf *mmapBuffer) Bytes() []byte {
	return mf.dt
}

func previewString(dt []byte) string {
	if bytes.Contains(dt, []byte{'\x00'}) {
		// Don't try to print binary data.
		// The null byte check is a simple heuristic for binary data.
		// It's not perfect, but good enough for our use case.
		return "<binary data>"
	}

	// dt could be large (especially since these are all mmaped files that get passed in).
	// we don't want to pass this through entirely.
	const maxPreview = 1024
	if len(dt) > maxPreview {
		return string(dt[:maxPreview]) + "<...truncated to 1024 bytes out of " + strconv.Itoa(len(dt)) + " bytes>"
	}
	return string(dt)
}
