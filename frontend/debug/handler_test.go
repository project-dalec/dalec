package debug

import (
	"context"
	"testing"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
)

func TestDepsOrScratch(t *testing.T) {
	ctx := context.Background()

	t.Run("nil", func(t *testing.T) {
		st := depsOrScratch(nil)
		if _, err := st.Marshal(ctx); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("non-nil", func(t *testing.T) {
		in := llb.Scratch().File(llb.Mkfile("/foo", 0o644, []byte("bar")))
		st := depsOrScratch(&in)
		def, err := st.Marshal(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(def.Def) == 0 {
			t.Fatal("expected the state to be passed through unchanged")
		}
	})
}

// A spec with no cargohome or gomod generators yields nil deps; the debug
// handlers must be able to marshal that without panicking.
func TestDepsNilForSpecWithoutGenerators(t *testing.T) {
	spec := &dalec.Spec{Name: "test"}
	worker := llb.Scratch()
	var sOpt dalec.SourceOpts

	if st := spec.CargohomeDeps(sOpt, worker); st != nil {
		t.Fatal("expected nil cargohome deps")
	}
	if st := spec.GomodDeps(sOpt, worker); st != nil {
		t.Fatal("expected nil gomod deps")
	}
	_ = depsOrScratch(spec.CargohomeDeps(sOpt, worker))
	_ = depsOrScratch(spec.GomodDeps(sOpt, worker))
}
