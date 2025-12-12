package testrunner

import (
	"context"
	"fmt"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/google/go-cmp/cmp"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/solver/pb"
	"github.com/project-dalec/dalec"
	"google.golang.org/protobuf/proto"
)

const (
	frontendMountPath      = "/tmp/internal/dalec/testrunner/frontend"
	internalStateMountPath = "/tmp/internal/dalec/testrunner/__internal_state"
)

func TestCheckFileExistsWithCheckLLB(t *testing.T) {
	t.Run("default flags", func(t *testing.T) {
		opts := []ValidationOpt{withTestFrontend()}
		opt := checkFileExists.WithCheck("/var/log/app.log", fileCheckFromYAML(t, "{}"), opts...)
		def := definitionFromStateOption(t, opt)
		exec := singleExecOp(t, def)
		wantedArgs := []string{
			frontendMountPath,
			testRunnerCmdName,
			string(checkFileExists),
			"--not=false",
			"--" + noFollowFlagName + "=false",
			"/var/log/app.log",
		}
		if diff := cmp.Diff(wantedArgs, exec.GetMeta().GetArgs()); diff != "" {
			t.Fatalf("unexpected exec args (-want +got):\n%s", diff)
		}
		requireMountDest(t, exec.GetMounts(), frontendMountPath)
		requireMountDest(t, exec.GetMounts(), internalStateMountPath)
	})

	t.Run("not exist and no follow", func(t *testing.T) {
		checker := fileCheckFromYAML(t, "not_exist: true\nno_follow: true\n")
		opt := checkFileExists.WithCheck("/tmp/link", checker, withTestFrontend())
		args := singleExecOp(t, definitionFromStateOption(t, opt)).GetMeta().GetArgs()
		if got := args[3:6]; !cmp.Equal(got, []string{"--not=true", "--" + noFollowFlagName + "=true", "/tmp/link"}) {
			t.Fatalf("unexpected args tail: %v", got)
		}
	})
}

func TestCheckFileContainsWithCheckLLB(t *testing.T) {
	checker := checkOutputFromYAML(t, "contains:\n  - alpha\n  - beta\n")
	stateOpts := checkFileContains.WithCheck("/tmp/output", checker, withTestFrontend())
	if len(stateOpts) != len(checker.Contains) {
		t.Fatalf("expected %d state options, got %d", len(checker.Contains), len(stateOpts))
	}

	for i, opt := range stateOpts {
		def := definitionFromStateOption(t, opt)
		exec := singleExecOp(t, def)
		want := []string{
			frontendMountPath,
			testRunnerCmdName,
			string(checkFileContains),
			"/tmp/output",
			checker.Contains[i],
		}
		if diff := cmp.Diff(want, exec.GetMeta().GetArgs()); diff != "" {
			t.Fatalf("unexpected exec args (-want +got):\n%s", diff)
		}
		requireMountDest(t, exec.GetMounts(), frontendMountPath)
		requireMountDest(t, exec.GetMounts(), internalStateMountPath)
	}
}

func TestCheckFileContainsWithCheckLLBSkip(t *testing.T) {
	if opts := checkFileContains.WithCheck("/tmp/output", checkOutputFromYAML(t, "{}"), withTestFrontend()); opts != nil {
		t.Fatalf("expected nil state options when contains is empty")
	}
}

func TestCheckFileMatchesWithCheckLLB(t *testing.T) {
	checker := checkOutputFromYAML(t, "matches:\n  - foo\n  - bar\n")
	stateOpts := checkFileMatches.WithCheck("/tmp/log", checker, withTestFrontend())
	if len(stateOpts) != 2 {
		t.Fatalf("expected 2 state options, got %d", len(stateOpts))
	}

	for i, opt := range stateOpts {
		exec := singleExecOp(t, definitionFromStateOption(t, opt))
		want := []string{frontendMountPath, testRunnerCmdName, string(checkFileMatches), "/tmp/log", checker.Matches[i]}
		if diff := cmp.Diff(want, exec.GetMeta().GetArgs()); diff != "" {
			t.Fatalf("unexpected exec args (-want +got):\n%s", diff)
		}
		requireMountDest(t, exec.GetMounts(), frontendMountPath)
		requireMountDest(t, exec.GetMounts(), internalStateMountPath)
	}
}

func TestCheckFileMatchesWithCheckSkip(t *testing.T) {
	if opts := checkFileMatches.WithCheck("/tmp/log", checkOutputFromYAML(t, "{}"), withTestFrontend()); opts != nil {
		t.Fatalf("expected nil state options when matches is empty")
	}
}

func TestCheckFileEqualsWithCheckLLB(t *testing.T) {
	t.Run("executes when equals set", func(t *testing.T) {
		checker := checkOutputFromYAML(t, "equals: data\n")
		exec := singleExecOp(t, definitionFromStateOption(t, checkFileEquals.WithCheck("/tmp/file", checker, withTestFrontend())))
		want := []string{frontendMountPath, testRunnerCmdName, string(checkFileEquals), "/tmp/file", "data"}
		if diff := cmp.Diff(want, exec.GetMeta().GetArgs()); diff != "" {
			t.Fatalf("unexpected exec args (-want +got):\n%s", diff)
		}
	})

	t.Run("skip when equals empty", func(t *testing.T) {
		checker := checkOutputFromYAML(t, "{}")
		execs := execsFromDefinition(t, definitionFromStateOption(t, checkFileEquals.WithCheck("/tmp/file", checker, withTestFrontend())))
		if len(execs) != 0 {
			t.Fatalf("expected no execs, got %d", len(execs))
		}
	})
}

func TestCheckFileStartsWithWithCheckLLB(t *testing.T) {
	checker := checkOutputFromYAML(t, "starts_with: hello\n")
	exec := singleExecOp(t, definitionFromStateOption(t, checkFileStartsWith.WithCheck("/tmp/out", checker, withTestFrontend())))
	want := []string{frontendMountPath, testRunnerCmdName, string(checkFileStartsWith), "/tmp/out", "hello"}
	if diff := cmp.Diff(want, exec.GetMeta().GetArgs()); diff != "" {
		t.Fatalf("unexpected exec args (-want +got):\n%s", diff)
	}
}

func TestCheckFileEndsWithWithCheckLLB(t *testing.T) {
	t.Run("runs when suffix provided", func(t *testing.T) {
		checker := checkOutputFromYAML(t, "ends_with: done\n")
		exec := singleExecOp(t, definitionFromStateOption(t, checkFileEndsWith.WithCheck("/tmp/out", checker, withTestFrontend())))
		want := []string{frontendMountPath, testRunnerCmdName, string(checkFileEndsWith), "/tmp/out", "done"}
		if diff := cmp.Diff(want, exec.GetMeta().GetArgs()); diff != "" {
			t.Fatalf("unexpected exec args (-want +got):\n%s", diff)
		}
	})

	t.Run("skip when suffix empty", func(t *testing.T) {
		execs := execsFromDefinition(t, definitionFromStateOption(t, checkFileEndsWith.WithCheck("/tmp/out", checkOutputFromYAML(t, "{}"), withTestFrontend())))
		if len(execs) != 0 {
			t.Fatalf("expected no execs, got %d", len(execs))
		}
	})
}

func TestCheckFileEmptyWithCheckLLB(t *testing.T) {
	t.Run("empty true", func(t *testing.T) {
		exec := singleExecOp(t, definitionFromStateOption(t, checkFileEmpty.WithCheck("/tmp/out", checkOutputFromYAML(t, "empty: true\n"), withTestFrontend())))
		want := []string{frontendMountPath, testRunnerCmdName, string(checkFileEmpty), "/tmp/out"}
		if diff := cmp.Diff(want, exec.GetMeta().GetArgs()); diff != "" {
			t.Fatalf("unexpected exec args (-want +got):\n%s", diff)
		}
	})

	t.Run("skip when empty false", func(t *testing.T) {
		execs := execsFromDefinition(t, definitionFromStateOption(t, checkFileEmpty.WithCheck("/tmp/out", checkOutputFromYAML(t, "{}"), withTestFrontend())))
		if len(execs) != 0 {
			t.Fatalf("expected no execs, got %d", len(execs))
		}
	})
}

func TestCheckFileIsDirWithCheckLLB(t *testing.T) {
	t.Run("is dir true", func(t *testing.T) {
		checker := fileCheckFromYAML(t, "is_dir: true\n")
		exec := singleExecOp(t, definitionFromStateOption(t, checkFileIsDir.WithCheck("/data", checker, withTestFrontend())))
		want := []string{frontendMountPath, testRunnerCmdName, string(checkFileIsDir), "/data"}
		if diff := cmp.Diff(want, exec.GetMeta().GetArgs()); diff != "" {
			t.Fatalf("unexpected exec args (-want +got):\n%s", diff)
		}
	})

	t.Run("skip when not dir", func(t *testing.T) {
		execs := execsFromDefinition(t, definitionFromStateOption(t, checkFileIsDir.WithCheck("/file", fileCheckFromYAML(t, "{}"), withTestFrontend())))
		if len(execs) != 0 {
			t.Fatalf("expected no execs, got %d", len(execs))
		}
	})
}

func TestCheckFilePermsWithCheckLLB(t *testing.T) {
	t.Run("runs when permissions set", func(t *testing.T) {
		checker := fileCheckFromYAML(t, "permissions: 0o754\n")
		exec := singleExecOp(t, definitionFromStateOption(t, checkFilePerms.WithCheck("/bin/tool", checker, withTestFrontend())))
		want := []string{frontendMountPath, testRunnerCmdName, string(checkFilePerms), "/bin/tool", fmt.Sprintf("%o", checker.Permissions.Perm())}
		if diff := cmp.Diff(want, exec.GetMeta().GetArgs()); diff != "" {
			t.Fatalf("unexpected exec args (-want +got):\n%s", diff)
		}
	})

	t.Run("skip when permissions unset", func(t *testing.T) {
		execs := execsFromDefinition(t, definitionFromStateOption(t, checkFilePerms.WithCheck("/bin/tool", fileCheckFromYAML(t, "{}"), withTestFrontend())))
		if len(execs) != 0 {
			t.Fatalf("expected no execs, got %d", len(execs))
		}
	})
}

func withTestFrontend() ValidationOpt {
	frontend := llb.Scratch().File(llb.Mkfile("frontend", 0o600, []byte("binary")))
	return func(vi *ValidationInfo) {
		st := frontend
		vi.Frontend = &st
	}
}

func definitionFromStateOption(t *testing.T, opt llb.StateOption) *llb.Definition {
	t.Helper()
	if opt == nil {
		t.Fatalf("state option must not be nil")
	}
	state := llb.Scratch().With(opt)
	def, err := state.Marshal(context.Background())
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	return def
}

func checkOutputFromYAML(t *testing.T, body string) *dalec.CheckOutput {
	t.Helper()
	var out dalec.CheckOutput
	if err := yaml.UnmarshalContext(context.Background(), []byte(body), &out); err != nil {
		t.Fatalf("unmarshal check output: %v", err)
	}
	return &out
}

func fileCheckFromYAML(t *testing.T, body string) *dalec.FileCheckOutput {
	t.Helper()
	var out dalec.FileCheckOutput
	if err := yaml.UnmarshalContext(context.Background(), []byte(body), &out); err != nil {
		t.Fatalf("unmarshal file check output: %v", err)
	}
	return &out
}

func singleExecOp(t *testing.T, def *llb.Definition) *pb.ExecOp {
	execs := execsFromDefinition(t, def)
	if len(execs) != 1 {
		t.Fatalf("expected 1 exec op, got %d", len(execs))
	}
	return execs[0]
}

func execsFromDefinition(t *testing.T, def *llb.Definition) []*pb.ExecOp {
	t.Helper()
	pbDef := def.ToPB()
	execs := make([]*pb.ExecOp, 0, len(pbDef.Def))
	for _, dt := range pbDef.Def {
		var op pb.Op
		if err := proto.Unmarshal(dt, &op); err != nil {
			t.Fatalf("unmarshal op: %v", err)
		}
		if exec := op.GetExec(); exec != nil {
			execs = append(execs, exec)
		}
	}
	return execs
}

func requireMountDest(t *testing.T, mounts []*pb.Mount, dest string) {
	t.Helper()
	for _, m := range mounts {
		if m.GetDest() == dest {
			return
		}
	}
	t.Fatalf("expected mount with dest %q, got %v", dest, mounts)
}
