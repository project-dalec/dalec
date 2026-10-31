package layersplit

import (
	"context"
	"flag"
	"fmt"
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

	// RPM dump file names read by the plan subcommand.
	RPMPackagesFile = "packages.tsv"
	RPMFilesFile    = "files.tsv"
	RPMVerifyFile   = "verify.txt"
	AppPackagesFile = "app.txt"

	maxReportedDiffs = 20
)

// Cmd runs the layer-split subcommands:
//
//	plan   --root DIR --dump DIR --out DIR [--max-layers N]
//	copy   --root DIR --plan DIR --out DIR [--dir-mtime SECONDS]
//	verify --expected DIR --actual DIR [--plan DIR]
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
	out := flags.String("out", "", "output directory")
	planDir := flags.String("plan", "", "directory with the plan")
	maxLayers := flags.Int("max-layers", DefaultMaxLayers, "maximum number of layers")
	dirMtime := flags.Int64("dir-mtime", 0, "timestamp for directories in layers, in seconds since the Unix epoch")
	expected := flags.String("expected", "", "expected root filesystem")
	actual := flags.String("actual", "", "actual root filesystem")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}

	switch args[0] {
	case "plan":
		return runPlan(*root, *dump, *out, *maxLayers)
	case "copy":
		return runCopy(*root, *planDir, *out, *dirMtime)
	case "verify":
		return runVerify(*expected, *actual, *planDir)
	default:
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func runPlan(root, dump, out string, maxLayers int) error {
	in, err := readRPMInputs(dump)
	if err != nil {
		return err
	}

	plan, err := BuildPlan(root, in, Options{MaxLayers: maxLayers})
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
		fmt.Printf("layer %d: %s %s (%d entries, %d bytes)\n", i, g.Kind, g.Name, g.Paths, g.Size)
	}
	return nil
}

func readRPMInputs(dump string) (Inputs, error) {
	var in Inputs

	open := func(name string) (*os.File, error) {
		return os.Open(filepath.Join(dump, name))
	}

	f, err := open(RPMPackagesFile)
	if err != nil {
		return in, err
	}
	in.Packages, err = ParseRPMPackages(f)
	f.Close()
	if err != nil {
		return in, fmt.Errorf("parsing %s: %w", RPMPackagesFile, err)
	}

	f, err = open(RPMFilesFile)
	if err != nil {
		return in, err
	}
	in.Files, err = ParseRPMFiles(f)
	f.Close()
	if err != nil {
		return in, fmt.Errorf("parsing %s: %w", RPMFilesFile, err)
	}

	f, err = open(RPMVerifyFile)
	if err != nil {
		return in, err
	}
	in.Modified, err = ParseRPMVerify(f)
	f.Close()
	if err != nil {
		return in, fmt.Errorf("parsing %s: %w", RPMVerifyFile, err)
	}

	f, err = open(AppPackagesFile)
	if err != nil {
		return in, err
	}
	in.App, err = ParseNames(f)
	f.Close()
	if err != nil {
		return in, fmt.Errorf("parsing %s: %w", AppPackagesFile, err)
	}
	return in, nil
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

// runVerify checks that actual (the merged layers) matches expected (the
// installed root filesystem).
//
// When planDir is set, modification times of paths in the state layer are not
// compared. BuildKit may reuse an earlier split when the root filesystem has
// the same content: its content checksum ignores modification times. Files
// owned by packages keep the timestamps recorded in the package database,
// which is part of that checksum, so they are always compared exactly. Files
// in the state layer are generated during install and their timestamps only
// reflect when the install ran.
func readStatePaths(planDir string) (map[string]bool, error) {
	dt, err := os.ReadFile(filepath.Join(planDir, PlanFile))
	if err != nil {
		return nil, err
	}
	plan, err := UnmarshalPlan(dt)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(filepath.Join(planDir, AssignFile))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	assign, err := ReadAssign(f)
	if err != nil {
		return nil, err
	}

	state := len(plan.Groups) - 1
	out := make(map[string]bool)
	for p, idx := range assign {
		if idx == state {
			out[p] = true
		}
	}
	return out, nil
}

func runVerify(expected, actual, planDir string) error {
	want, err := Tree(expected)
	if err != nil {
		return fmt.Errorf("reading expected tree: %w", err)
	}
	got, err := Tree(actual)
	if err != nil {
		return fmt.Errorf("reading actual tree: %w", err)
	}

	if planDir != "" {
		statePaths, err := readStatePaths(planDir)
		if err != nil {
			return err
		}
		ClearMtimes(want, statePaths)
		ClearMtimes(got, statePaths)
	}

	diffs := Compare(want, got)
	if len(diffs) == 0 {
		fmt.Printf("layer split verified: %d entries match\n", len(want))
		return nil
	}

	fmt.Fprintf(os.Stderr, "layer split verification failed: %d paths differ from the installed root filesystem\n", len(diffs))
	for i, d := range diffs {
		if i == maxReportedDiffs {
			fmt.Fprintf(os.Stderr, "  ... and %d more\n", len(diffs)-maxReportedDiffs)
			break
		}
		fmt.Fprintln(os.Stderr, "  "+d.String())
	}
	fmt.Fprintln(os.Stderr, "Workaround: set image.layers to single (or unset DALEC_IMAGE_LAYERS) and please file an issue.")
	return fmt.Errorf("layer split verification failed")
}
