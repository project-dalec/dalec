package testrunner

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"
	"github.com/project-dalec/dalec"
)

const CheckFilesCmdName = "test-checkfiles"

// CheckFilesCmd is the entrypoint for the file checking subcommand.
// It reads the file checks from the provided file path (first argument)
// and executes them, writing output to os.Stdout and os.Stderr.
//
// This should only be called from inside a container where the test is meant to run.
func CheckFilesCmd(args []string) {
	var files map[string]dalec.FileCheckOutput

	flags := flag.NewFlagSet(CheckFilesCmdName, flag.ExitOnError)

	var outputPath string
	flags.StringVar(&outputPath, "output", "", "Path to write test results to")

	if err := flags.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "error parsing flags:", err)
		os.Exit(1)
	}

	if outputPath == "" {
		fmt.Fprintln(os.Stderr, "error: output path is required")
		os.Exit(1)
	}

	dt, err := os.ReadFile(flags.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error reading file checks:", err)
		os.Exit(1)
	}

	if err := json.Unmarshal(dt, &files); err != nil {
		fmt.Fprintln(os.Stderr, "error unmarshalling file checks:", err)
		os.Exit(1)
	}

	results := checkFiles(files)
	if len(results) == 0 {
		return
	}

	dt, err = json.Marshal(results)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error marshaling results:", err)
		os.Exit(2)
	}

	if err := writeFileAppend(outputPath, dt, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "error writing results:", err)
		os.Exit(2)
	}
}

func writeFileAppend(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, mode)
	if err != nil {
		return errors.Wrapf(err, "opening file %s", path)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return errors.Wrapf(err, "writing file %s", path)
	}
	return nil
}

type FileCheckErrResult struct {
	Filename  string
	StepIndex *int
	Checks    []*dalec.CheckOutputError
}

func checkFiles(files map[string]dalec.FileCheckOutput) []FileCheckErrResult {
	var results []FileCheckErrResult
	for path, check := range files {
		if err := checkFile(path, check); err != nil {
			results = append(results, FileCheckErrResult{Filename: path, Checks: getFileCheckErrs(err)})
		}
	}
	return results
}

func checkFile(p string, check dalec.FileCheckOutput) error {
	var (
		stat os.FileInfo
		err  error
	)

	if check.NoFollow {
		stat, err = os.Lstat(p)
	} else {
		stat, err = os.Stat(p)
	}

	if err != nil {
		if os.IsNotExist(err) {
			if check.NotExist {
				return nil
			}

			return &dalec.CheckOutputError{
				Kind:     dalec.CheckFileNotExistsKind,
				Path:     p,
				Expected: "exists=true",
				Actual:   "exists=false",
			}
		}
		return errors.Wrapf(err, "checking file %s", p)
	}

	var target string
	if check.LinkTarget != "" {
		target, err = os.Readlink(p)
		if err != nil {
			return errors.Wrapf(err, "reading symlink %s", p)
		}
	}

	if check.NotExist {
		return &dalec.CheckOutputError{
			Kind:     dalec.CheckFileNotExistsKind,
			Path:     p,
			Expected: "exists=false",
			Actual:   "exists=true",
		}
	}

	var v string
	if !stat.IsDir() && !check.IsEmpty() {
		f, err := os.Open(p)
		if err != nil {
			return errors.Wrapf(err, "opening file %s", p)
		}
		defer f.Close()
		dt, err := io.ReadAll(f)
		if err != nil {
			return errors.Wrapf(err, "reading file %s", p)
		}
		v = string(dt)
	}

	if err := check.Check(v, stat.Mode(), stat.IsDir(), p, target); err != nil {
		return err
	}
	return nil
}

// WithFileChecks returns an llb.RunOption that checks the files specified in the test spec.
func WithFileChecks(frontend llb.State, test *dalec.TestSpec, outputPath string) llb.RunOption {
	return dalec.RunOptFunc(func(ei *llb.ExecInfo) {
		llb.WithCustomNamef("Check files for test %q", test.Name).SetRunOption(ei)

		dt, err := json.Marshal(test.Files)
		if err != nil {
			ei.State = dalec.ErrorState(ei.State, fmt.Errorf("failed to marshal file checks for test %q: %w", test.Name, err))
			llb.Args([]string{"false"}).SetRunOption(ei)
			return
		}

		const checkFilesPath = "/tmp/dalec/internal/frontend/test/check_files"
		llb.AddMount(checkFilesPath, frontend, llb.SourcePath("/frontend")).SetRunOption(ei)
		st := llb.Scratch().File(llb.Mkfile("files.json", 0o600, dt))

		const fileChecksPath = "/tmp/dalec/internal/frontend/test/files.json"
		llb.AddMount(fileChecksPath, st, llb.SourcePath("files.json")).SetRunOption(ei)
		llb.Args([]string{checkFilesPath, CheckFilesCmdName, "--output", outputPath, fileChecksPath, test.Name}).SetRunOption(ei)
	})
}

func getFileCheckErrs(err error) []*dalec.CheckOutputError {
	if wrapped, ok := err.(interface{ Unwrap() []error }); ok {
		var errs []*dalec.CheckOutputError
		for _, e := range wrapped.Unwrap() {
			errs = append(errs, getFileCheckErrs(e)...)
		}
		return errs
	}

	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return getFileCheckErrs(wrapped.Unwrap())
	}

	var ce *dalec.CheckOutputError
	ok := errors.As(err, &ce)
	if !ok {
		ce = &dalec.CheckOutputError{
			Kind:   "unknown",
			Actual: err.Error(),
		}
	}
	return []*dalec.CheckOutputError{ce}
}
