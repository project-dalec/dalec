package test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
)

// LLBOpsFromState extracts the list of LLB operations from the provided LLB state.
//
// This function and LLBOp type has been inspired by
// https://github.com/moby/buildkit/blob/c70e8e666f8f6ee3c0d83b20c338be5aedeaa97a/cmd/buildctl/debug/dumpllb.go#L59.
func LLBOpsFromState(ctx context.Context, state llb.State) ([]LLBOp, error) {
	def, err := state.Marshal(ctx)
	if err != nil {
		return nil, fmt.Errorf("marshaling state: %w", err)
	}

	var ops []LLBOp
	for _, dt := range def.Def {
		var op pb.Op
		if err := op.UnmarshalVT(dt); err != nil {
			return nil, fmt.Errorf("parsing op: %w", err)
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

// LLBOpsToJSON converts a list of LLB operations to a JSON string.
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

// LLBOp represents a single LLB operation along with its metadata.
type LLBOp struct {
	Op         *pb.Op
	OpMetadata *pb.OpMetadata
}
