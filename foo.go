package dalec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/bklog"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
)

type RateOP func(LLBOp) bool

func LogConst(name string, opts ...llb.ConstraintsOpt) {
	c := llb.NewConstraints(opts...)

	if c.Metadata.ProgressGroup != nil {
		return
	}

	raw, err := json.Marshal(c)
	if err != nil {
		panic(err)
	}

	bklog.G(context.TODO()).WithField("f", "invidian").WithField("LogConst", name).Info(string(raw))
}

func LogBadOps(name string, f RateOP, state llb.State) {
	ops, err := LLBOpsFromState(context.TODO(), state)
	if err != nil {
		panic(err)
	}

	badOps := []LLBOp{}

	for _, op := range ops {
		if f(op) {
			badOps = append(badOps, op)
		}
	}

	if len(badOps) > 0 {
		opJSON, err := LLBOpsToJSON(badOps)
		if err != nil {
			panic(err)
		}

		bklog.G(context.TODO()).WithField("f", "invidian").WithField("LogBadOps", name).Info(opJSON)
	}
}

func LLBOpsFromState(ctx context.Context, state llb.State) ([]LLBOp, error) {
	def, err := state.Marshal(ctx)
	if err != nil {
		return nil, fmt.Errorf("marshaling state: %w", err)
	}

	var ops []LLBOp
	for _, dt := range def.Def {
		var op pb.Op
		if err := op.UnmarshalVT(dt); err != nil {
			return nil, errors.Wrap(err, "failed to parse op")
		}
		dgst := digest.FromBytes(dt)
		ent := LLBOp{Op: &op, OpMetadata: def.Metadata[dgst].ToPB()}

		ops = append(ops, ent)
	}

	if len(ops) > 0 {
		ops = ops[:len(ops)-1] // Last operation is a final export, it has no operations.
	}

	return ops, nil
}

func LLBOpsToJSON(ops []LLBOp) (string, error) {
	var buf bytes.Buffer

	enc := json.NewEncoder(&buf)
	for _, op := range ops {
		if err := enc.Encode(op); err != nil {
			return "", err
		}
	}

	return buf.String(), nil
}

type LLBOp struct {
	Op         *pb.Op
	OpMetadata *pb.OpMetadata
}
