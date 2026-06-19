package testrunner

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
)

const CmdName = "test-runner"

type Runner struct{}

func (tr *Runner) Cmd(ctx context.Context, args []string) {
	flags := flag.NewFlagSet(CmdName, flag.ExitOnError)
	flags.Parse(args) //nolint:errcheck // errors are handled by ExitOnError

	if flags.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "no test-runner command provided")
		exit(1)
	}

	cmd := flags.Arg(0)
	args = flags.Args()[1:]
	switch cmd {
	case string(checkFilePerms):
		checkFilePerms.Cmd(args)
	case string(checkFileNotExists):
		checkFileNotExists.Cmd(args)
	case string(checkFileContains):
		checkFileContains.Cmd(args)
	case string(checkFileEmpty):
		checkFileEmpty.Cmd(args)
	case string(checkFileStartsWith):
		checkFileStartsWith.Cmd(args)
	case string(checkFileEndsWith):
		checkFileEndsWith.Cmd(args)
	case string(checkFileMatches):
		checkFileMatches.Cmd(args)
	case string(checkFileEquals):
		checkFileEquals.Cmd(args)
	case string(checkFileIsDir):
		checkFileIsDir.Cmd(args)
	case string(checkFileLinkTarget):
		checkFileLinkTarget.Cmd(args)
	case string(stepRunner):
		stepRunner.Cmd(ctx, args)
	case string(trueCmd):
		trueCmd.Cmd(args)
	default:
		fmt.Fprintln(os.Stderr, CmdName+":", "Unknown command:", cmd)
		exit(70) // 70 is EX_SOFTWARE, meaning internal software error occurred
	}
}

func doValidate(args []string, opts ...ValidationOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		const (
			internalStatePath = "/tmp/internal/dalec/testrunner/__internal_state"
		)

		return in.Run(
			testRunner(args, opts...),
			llb.ReadonlyRootFS(),
			llb.WithCustomName(strings.Join(args, " ")),
		).AddMount(internalStatePath, in)
	}
}

func testRunner(args []string, opts ...ValidationOpt) llb.RunOption {
	return dalec.RunOptFunc(func(ei *llb.ExecInfo) {
		const p = "/tmp/internal/dalec/testrunner/frontend"

		args = append([]string{p, CmdName}, args...)
		llb.Args(args).SetRunOption(ei)

		info := validationOpts(opts...)
		llb.AddMount(p, *info.Frontend, llb.Readonly, llb.SourcePath("/frontend")).SetRunOption(ei)
		info.SetRunOption(ei)
	})
}

type ValidationOpt func(*ValidationInfo)

type ValidationInfo struct {
	Frontend    *llb.State
	Constraints []llb.ConstraintsOpt
	ExtraOpts   []llb.RunOption
}

func validationOptsFromTest(t *dalec.TestSpec, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) ValidationOpt {
	return func(i *ValidationInfo) {
		for k, v := range t.Env {
			i.ExtraOpts = append(i.ExtraOpts, llb.AddEnv(k, v))
		}

		for _, mnt := range t.Mounts {
			i.ExtraOpts = append(i.ExtraOpts, mnt.ToRunOption(sOpt, dalec.WithConstraints(opts...)))
		}
	}
}

func ValidateWithMount(sOpt dalec.SourceOpts, mnt dalec.SourceMount, opts ...llb.ConstraintsOpt) ValidationOpt {
	return func(i *ValidationInfo) {
		i.ExtraOpts = append(i.ExtraOpts, mnt.ToRunOption(sOpt, dalec.WithConstraints(opts...)))
	}
}

func (i *ValidationInfo) SetRunOption(ei *llb.ExecInfo) {
	c := ei.Constraints
	i.SetConstraintsOption(&c)
	ei.Constraints = c

	for _, opt := range i.ExtraOpts {
		opt.SetRunOption(ei)
	}
}

func (i *ValidationInfo) SetConstraintsOption(c *llb.Constraints) {
	for _, opt := range i.Constraints {
		opt.SetConstraintsOption(c)
	}
}

func WithConstraints(opts ...llb.ConstraintsOpt) ValidationOpt {
	return func(vi *ValidationInfo) {
		vi.Constraints = append(vi.Constraints, opts...)
	}
}

func validationOpts(opts ...ValidationOpt) ValidationInfo {
	var i ValidationInfo
	for _, o := range opts {
		o(&i)
	}

	if i.Frontend == nil {
		panic("missing frontend state in validation opt")
	}

	return i
}

func asConstraints(opts ...ValidationOpt) llb.ConstraintsOpt {
	return dalec.ConstraintsOptFunc(func(c *llb.Constraints) {
		vi := validationOpts(opts...)
		vi.SetConstraintsOption(c)
	})
}

// requireValidations returns a state option that ensures every validation in
// stateOpts is evaluated against the input state without letting any of them
// affect the output filesystem.
//
// Each validation is applied independently to the same input state (no
// validation sees another's effects). Validations are side-effect only: they
// assert things about the input and their resulting filesystems are required
// purely so buildkit executes them.
//
// Like [requireStates], it requires the validations via the PassthroughOp when
// the daemon supports it. Because the validations are side-effect-only states
// derived from the same input, the backwards-compatible fallback can simply
// diff each one against the input and merge the (effectively empty) diffs back
// together, which forces their evaluation while yielding a filesystem
// equivalent to the input:
//
//	         input
//	           |
//	   +-------+-------+
//	   |       |       |
//	apply    apply   apply
//	 opt1     opt2    opt3
//	   |       |       |
//	 diff1  diff2  diff3
//	   |       |       |
//	   +-------+-------+
//	           |
//	merge(input, diff1, diff2, diff3)
//	           |
//	         output
func requireValidations(stateOpts []llb.StateOption, opts ...ValidationOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		if len(stateOpts) == 0 {
			return in
		}

		if len(stateOpts) == 1 {
			// Avoid diff/merge overhead for single state option
			return in.With(stateOpts[0])
		}

		if dalec.PassthroughOpSupported() {
			deps := make([]llb.State, 0, len(stateOpts))
			for _, o := range stateOpts {
				deps = append(deps, in.With(o))
			}
			return in.Requires(validationRequiresID, deps...)
		}

		states := make([]llb.State, 0, len(opts))
		states = append(states, in)
		for _, o := range stateOpts {
			states = append(states, in.With(o))
		}

		return dalec.MergeAtPath(in, states, "/", asConstraints(opts...))
	}
}

func filterPath(p string) string {
	if strings.HasPrefix(p, "/tmp/internal/dalec") {
		return filepath.Base(p)
	}
	return p
}

// requireStates returns out, ensuring every state in deps is evaluated as part
// of building out.
//
// When the buildkit daemon supports the PassthroughOp (buildkit v0.31.0+) the
// dependency is expressed directly via [llb.State.Requires], which returns out
// unchanged while declaring deps as required inputs. This needs no extra exec
// and lets independent validations build in parallel.
//
// Otherwise it falls back to the older behavior: the deps are combined into a
// single state (so one op depends on all of them) and a no-op command is run to
// force their evaluation while yielding out. This is a hack to work around the
// fact that older buildkit does not have a proper way to express "run this for
// validation only".
func requireStates(id string, out llb.State, deps []llb.State, opts ...ValidationOpt) llb.State {
	if len(deps) == 0 {
		return out
	}

	if dalec.PassthroughOpSupported() {
		return out.Requires(id, deps...)
	}

	forced := deps[0]
	if len(deps) > 1 {
		forced = dalec.MergeAtPath(deps[0], deps[1:], "/", asConstraints(opts...))
	}
	return forced.With(trueCmd.WithOutput(out, opts...))
}

// WithFinalState returns a state option which takes as input a potentially modified
// state and returns the original unmodified state st.
// This makes sure that any changes made during test steps are discarded but makes sure
// there is a dependency on the intermediate state so buildkit will execute it.
func WithFinalState(st llb.State, opts ...ValidationOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		return requireStates(finalStateRequiresID, st, []llb.State{in}, opts...)
	}
}

// finalStateRequiresID is the opaque identifier used for the PassthroughOp
// created by [WithFinalState].
const finalStateRequiresID = "dalec.testrunner.requires"

// testsRequiresID is the opaque identifier used for the PassthroughOp created by
// [WithTests] when requiring all of a target's tests.
const testsRequiresID = "dalec.testrunner.tests.requires"

// validationRequiresID is the opaque identifier used for the PassthroughOp
// created by [requireValidations] when requiring a set of side-effect-only
// validations.
const validationRequiresID = "dalec.testrunner.validation.requires"

func withCheckOutput(filename string, checker *dalec.CheckOutput, opts ...ValidationOpt) []llb.StateOption {
	if checker.IsEmpty() {
		return nil
	}

	var outs []llb.StateOption

	outs = append(outs, checkFileEmpty.WithCheck(filename, checker, opts...))
	outs = append(outs, checkFileEquals.WithCheck(filename, checker, opts...))
	outs = append(outs, checkFileContains.WithCheck(filename, checker, opts...)...)
	outs = append(outs, checkFileMatches.WithCheck(filename, checker, opts...)...)
	outs = append(outs, checkFileStartsWith.WithCheck(filename, checker, opts...))
	outs = append(outs, checkFileEndsWith.WithCheck(filename, checker, opts...))

	return outs
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
