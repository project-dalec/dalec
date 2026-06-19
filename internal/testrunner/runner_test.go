package testrunner

import (
	"context"
	"strings"
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

// countReadonlyMountsWithPrefix counts read-only exec mounts whose destination
// is under prefix. The forcing exec mounts every required state read-only under
// requiresMountPrefix, so this reports how many states it is forcing via mounts.
func countReadonlyMountsWithPrefix(t *testing.T, def *llb.Definition, prefix string) int {
	t.Helper()
	var n int
	for _, dt := range def.ToPB().Def {
		var op pb.Op
		assert.NilError(t, proto.Unmarshal(dt, &op))
		exec := op.GetExec()
		if exec == nil {
			continue
		}
		for _, m := range exec.Mounts {
			if m.Readonly && strings.HasPrefix(m.Dest, prefix) {
				n++
			}
		}
	}
	return n
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

func TestRequireStates(t *testing.T) {
	out := llb.Image("example.com/out:latest")
	deps := []llb.State{
		llb.Image("example.com/dep1:latest"),
		llb.Image("example.com/dep2:latest"),
		llb.Image("example.com/dep3:latest"),
	}

	t.Run("no deps returns out unchanged", func(t *testing.T) {
		dalec.SetPassthroughOpSupported(false)

		st := requireStates("test.id", out, nil)
		def, err := st.Marshal(context.Background())
		assert.NilError(t, err)

		passthrough, exec := countOps(t, def)
		assert.Equal(t, passthrough, 0, "expected no passthrough op")
		assert.Equal(t, exec, 0, "expected no exec op")
	})

	t.Run("passthrough supported requires all deps in one op", func(t *testing.T) {
		t.Cleanup(func() { dalec.SetPassthroughOpSupported(false) })
		dalec.SetPassthroughOpSupported(true)

		// A single passthrough op returns out while requiring every dep, so no
		// exec op is emitted.
		st := requireStates("test.id", out, deps)
		def, err := st.Marshal(context.Background())
		assert.NilError(t, err)

		passthrough, exec := countOps(t, def)
		assert.Equal(t, passthrough, 1, "expected exactly one passthrough op")
		assert.Equal(t, exec, 0, "expected no exec ops in the passthrough path")
	})

	t.Run("passthrough unsupported forces deps via read-only mounts", func(t *testing.T) {
		dalec.SetPassthroughOpSupported(false)

		st := requireStates("test.id", out, deps, withTestFrontend())
		def, err := st.Marshal(context.Background())
		assert.NilError(t, err)

		passthrough, exec := countOps(t, def)
		assert.Equal(t, passthrough, 0, "expected no passthrough op in the fallback path")
		assert.Assert(t, exec >= 1, "expected at least one exec op in the fallback path")

		// deps[0] is the exec rootfs; the remaining deps are mounted read-only.
		mounts := countReadonlyMountsWithPrefix(t, def, requiresMountPrefix)
		assert.Equal(t, mounts, len(deps)-1, "expected every dep after the first to be mounted read-only")
	})

	t.Run("passthrough unsupported single dep needs no extra mounts", func(t *testing.T) {
		dalec.SetPassthroughOpSupported(false)

		st := requireStates("test.id", out, deps[:1], withTestFrontend())
		def, err := st.Marshal(context.Background())
		assert.NilError(t, err)

		passthrough, exec := countOps(t, def)
		assert.Equal(t, passthrough, 0, "expected no passthrough op in the fallback path")
		assert.Assert(t, exec >= 1, "expected at least one exec op in the fallback path")

		mounts := countReadonlyMountsWithPrefix(t, def, requiresMountPrefix)
		assert.Equal(t, mounts, 0, "a single dep is the rootfs and needs no extra mounts")
	})
}

func TestRequireValidations(t *testing.T) {
	in := llb.Image("example.com/in:latest")

	// Each validation produces a distinct derived state, standing in for the
	// side-effect-only file/output checks the test runner emits.
	stateOpts := []llb.StateOption{
		func(s llb.State) llb.State { return s.File(llb.Mkfile("/v1", 0o644, []byte("1"))) },
		func(s llb.State) llb.State { return s.File(llb.Mkfile("/v2", 0o644, []byte("2"))) },
		func(s llb.State) llb.State { return s.File(llb.Mkfile("/v3", 0o644, []byte("3"))) },
	}

	t.Run("passthrough supported requires validations in one passthrough op", func(t *testing.T) {
		t.Cleanup(func() { dalec.SetPassthroughOpSupported(false) })
		dalec.SetPassthroughOpSupported(true)

		// On the passthrough path the validations become required inputs of a
		// single passthrough op, so no exec is emitted.
		st := in.With(requireValidations(stateOpts))
		def, err := st.Marshal(context.Background())
		assert.NilError(t, err)

		passthrough, exec := countOps(t, def)
		assert.Equal(t, passthrough, 1, "expected exactly one passthrough op")
		assert.Equal(t, exec, 0, "expected no exec ops on the passthrough path")
	})

	t.Run("passthrough unsupported forces validations via read-only mounts", func(t *testing.T) {
		dalec.SetPassthroughOpSupported(false)

		st := in.With(requireValidations(stateOpts, withTestFrontend()))
		def, err := st.Marshal(context.Background())
		assert.NilError(t, err)

		passthrough, exec := countOps(t, def)
		assert.Equal(t, passthrough, 0, "expected no passthrough op on the fallback path")
		assert.Assert(t, exec >= 1, "expected at least one exec op on the fallback path")

		// The first validation state is the exec rootfs; the rest are mounted.
		mounts := countReadonlyMountsWithPrefix(t, def, requiresMountPrefix)
		assert.Equal(t, mounts, len(stateOpts)-1, "expected every validation after the first to be mounted read-only")
	})

	t.Run("no validations returns input unchanged", func(t *testing.T) {
		dalec.SetPassthroughOpSupported(true)
		t.Cleanup(func() { dalec.SetPassthroughOpSupported(false) })

		st := in.With(requireValidations(nil))
		def, err := st.Marshal(context.Background())
		assert.NilError(t, err)

		passthrough, exec := countOps(t, def)
		assert.Equal(t, passthrough, 0, "expected no passthrough op with no validations")
		assert.Equal(t, exec, 0, "expected no exec op with no validations")
	})

	t.Run("single validation needs no passthrough op", func(t *testing.T) {
		dalec.SetPassthroughOpSupported(true)
		t.Cleanup(func() { dalec.SetPassthroughOpSupported(false) })

		// A single validation already depends on the input, so no passthrough
		// op is needed.
		st := in.With(requireValidations(stateOpts[:1]))
		def, err := st.Marshal(context.Background())
		assert.NilError(t, err)

		passthrough, _ := countOps(t, def)
		assert.Equal(t, passthrough, 0, "single validation needs no passthrough op")
	})
}
