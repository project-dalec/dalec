package layersplit

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
)

// CmdName is the frontend subcommand used to run the layer split helpers
// inside build containers.
const CmdName = "layer-split"

const (
	// PlanFile is the name of the plan file written by the plan subcommand.
	PlanFile = "plan.json"
	// AssignFile is the name of the assignment file written by the plan subcommand.
	AssignFile = "assign"

	// Package manager data read by the plan subcommand from the dump
	// directory.

	// PackagesFile lists the installed package names, one per line.
	PackagesFile = "packages.txt"
	// FilesFile lists the files of installed packages, in the format read by
	// [ParseRPMFiles].
	FilesFile = "files.tsv"
	// VerifyFile lists the files changed since install, in the format read by
	// [ParseRPMVerify].
	VerifyFile = "verify.txt"
	// AppPackagesFile lists the names of the packages built from the spec,
	// one per line.
	AppPackagesFile = "app.txt"
	// RPMRequiresFile and RPMProvidesFile hold RPM dependency data, read by
	// [ParseRPMRequires] and [ParseRPMProvides].
	RPMRequiresFile = "rpm-requires.tsv"
	RPMProvidesFile = "rpm-provides.tsv"
	// DpkgDepsFile holds dpkg dependency data, read by [ParseDpkgDeps].
	DpkgDepsFile = "dpkg-deps.tsv"
	// InstalledAtFile lists when each package was installed, as
	// "seconds\tname" lines, read by [ParseInstalledAt]. It is optional.
	InstalledAtFile = "installed-at.tsv"

	maxReportedDiffs = 20
)

// Cmd runs the layer-split subcommands:
//
//	plan   --root DIR --dump DIR --base FILE --out DIR
//	copy   --root DIR --plan DIR --out DIR [--dir-mtime SECONDS]
//	verify --expected DIR --actual DIR
func Cmd(_ context.Context, args []string) {
	if err := run(args); err != nil {
		fmt.Fprintln(os.Stderr, CmdName+":", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("missing subcommand")
	}

	flags := flag.NewFlagSet(CmdName+" "+args[0], flag.ContinueOnError)
	root := flags.String("root", "", "root filesystem")
	dump := flags.String("dump", "", "directory with package manager data")
	base := flags.String("base", "", "file listing the distro's base package names, one per line")
	out := flags.String("out", "", "output directory")
	planDir := flags.String("plan", "", "directory with the plan")
	dirMtime := flags.Int64("dir-mtime", 0, "timestamp for directories in layers, in seconds since the Unix epoch")
	expected := flags.String("expected", "", "expected root filesystem")
	actual := flags.String("actual", "", "actual root filesystem")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}

	switch args[0] {
	case "plan":
		return runPlan(*root, *dump, *base, *out)
	case "copy":
		return runCopy(*root, *planDir, *out, *dirMtime)
	case "verify":
		return runVerify(*expected, *actual)
	default:
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func runPlan(root, dump, base, out string) error {
	in, err := readInputs(dump, base)
	if err != nil {
		return err
	}

	plan, err := BuildPlan(root, in)
	if err != nil {
		return err
	}

	dt, err := MarshalPlan(plan)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, PlanFile), dt, 0o644); err != nil {
		return err
	}

	f, err := os.Create(filepath.Join(out, AssignFile))
	if err != nil {
		return err
	}
	if err := WriteAssign(f, plan.Assign); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	for i, g := range plan.Groups {
		fmt.Printf("layer %d: %s (%d packages, %d entries, %d bytes)\n", i, g.Kind, len(g.Packages), g.Paths, g.Size)
	}
	return nil
}

func readInputs(dump, base string) (Inputs, error) {
	var in Inputs

	read := func(path string, parse func(io.Reader) error) error {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		if err := parse(f); err != nil {
			return fmt.Errorf("parsing %s: %w", filepath.Base(path), err)
		}
		return nil
	}
	dumpFile := func(name string) string { return filepath.Join(dump, name) }

	err := errors.Join(
		read(dumpFile(PackagesFile), func(r io.Reader) (err error) {
			in.Packages, err = ParseNames(r)
			return err
		}),
		read(dumpFile(FilesFile), func(r io.Reader) (err error) {
			in.Files, err = ParseRPMFiles(r)
			return err
		}),
		read(dumpFile(VerifyFile), func(r io.Reader) (err error) {
			in.Modified, err = ParseRPMVerify(r)
			return err
		}),
		read(dumpFile(AppPackagesFile), func(r io.Reader) (err error) {
			in.App, err = ParseNames(r)
			return err
		}),
		read(base, func(r io.Reader) (err error) {
			in.Base, err = ParseNames(r)
			return err
		}),
	)
	if err != nil {
		return in, err
	}

	if _, err := os.Stat(dumpFile(InstalledAtFile)); err == nil {
		err := read(dumpFile(InstalledAtFile), func(r io.Reader) (err error) {
			in.InstalledAt, err = ParseInstalledAt(r)
			return err
		})
		if err != nil {
			return in, err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return in, err
	}

	if _, err := os.Stat(dumpFile(DpkgDepsFile)); err == nil {
		err := read(dumpFile(DpkgDepsFile), func(r io.Reader) error { return ParseDpkgDeps(r, &in.Deps) })
		return in, err
	} else if !errors.Is(err, fs.ErrNotExist) {
		return in, err
	}
	err = errors.Join(
		read(dumpFile(RPMRequiresFile), func(r io.Reader) error { return ParseRPMRequires(r, &in.Deps) }),
		read(dumpFile(RPMProvidesFile), func(r io.Reader) error { return ParseRPMProvides(r, &in.Deps) }),
	)
	return in, err
}

func runCopy(root, planDir, out string, dirMtime int64) error {
	dt, err := os.ReadFile(filepath.Join(planDir, PlanFile))
	if err != nil {
		return err
	}
	plan, err := UnmarshalPlan(dt)
	if err != nil {
		return err
	}

	f, err := os.Open(filepath.Join(planDir, AssignFile))
	if err != nil {
		return err
	}
	assign, err := ReadAssign(f)
	f.Close()
	if err != nil {
		return err
	}

	outs := make([]string, len(plan.Groups))
	for i := range plan.Groups {
		outs[i] = filepath.Join(out, strconv.Itoa(i))
	}
	return Copy(root, assign, outs, dirMtime)
}

func runVerify(expected, actual string) error {
	want, err := Tree(expected)
	if err != nil {
		return fmt.Errorf("reading expected tree: %w", err)
	}
	got, err := Tree(actual)
	if err != nil {
		return fmt.Errorf("reading actual tree: %w", err)
	}

	diffs := Compare(want, got)
	if len(diffs) == 0 {
		fmt.Printf("image layers verified: %d entries match\n", len(want))
		return nil
	}

	fmt.Fprintf(os.Stderr, "image layer verification failed: %d paths differ from the installed root filesystem\n", len(diffs))
	for i, d := range diffs {
		if i == maxReportedDiffs {
			fmt.Fprintf(os.Stderr, "  ... and %d more\n", len(diffs)-maxReportedDiffs)
			break
		}
		fmt.Fprintln(os.Stderr, "  "+d.String())
	}
	fmt.Fprintln(os.Stderr, "Please file an issue at https://github.com/project-dalec/dalec/issues.")
	return fmt.Errorf("image layer verification failed")
}
