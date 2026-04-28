package testrunner

import (
	"context"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/gogo/protobuf/proto"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/solver/pb"
	"github.com/project-dalec/dalec"
	"gotest.tools/v3/assert"
)

const (
	frontendMountPath      = "/tmp/internal/dalec/testrunner/frontend"
	internalStateMountPath = "/tmp/internal/dalec/testrunner/__internal_state"
)

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
	t.Helper()
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
		err := proto.Unmarshal(dt, &op)
		assert.NilError(t, err)
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
