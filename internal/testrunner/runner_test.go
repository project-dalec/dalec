package testrunner

import (
	"context"
	"testing"

	"github.com/gogo/protobuf/proto"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/solver/pb"
	"github.com/project-dalec/dalec"
	"gotest.tools/v3/assert"
)

func countOps(t *testing.T, def *llb.Definition) (passthrough, exec int) {
	t.Helper()
	for _, dt := range def.ToPB().Def {
		var op pb.Op
		assert.NilError(t, proto.Unmarshal(dt, &op))
		if op.GetPassthrough() != nil {
			passthrough++
		}
		if op.GetExec() != nil {
			exec++
		}
	}
	return passthrough, exec
}

func TestWithFinalState(t *testing.T) {
	orig := llb.Image("example.com/orig:latest")
	validation := llb.Image("example.com/validation:latest")

	t.Run("passthrough supported uses PassthroughOp", func(t *testing.T) {
		t.Cleanup(func() { dalec.SetPassthroughOpSupported(false) })
		dalec.SetPassthroughOpSupported(true)

		// The passthrough path returns the original state while requiring the
		// validation state, so it needs no frontend binary mount and emits no
		// exec op.
		st := validation.With(WithFinalState(orig))
		def, err := st.Marshal(context.Background())
		assert.NilError(t, err)

		passthrough, exec := countOps(t, def)
		assert.Equal(t, passthrough, 1, "expected exactly one passthrough op")
		assert.Equal(t, exec, 0, "expected no exec ops in the passthrough path")
	})

	t.Run("passthrough unsupported falls back to exec", func(t *testing.T) {
		dalec.SetPassthroughOpSupported(false)

		st := validation.With(WithFinalState(orig, withTestFrontend()))
		def, err := st.Marshal(context.Background())
		assert.NilError(t, err)

		passthrough, exec := countOps(t, def)
		assert.Equal(t, passthrough, 0, "expected no passthrough op in the fallback path")
		assert.Assert(t, exec >= 1, "expected at least one exec op in the fallback path")
	})
}
