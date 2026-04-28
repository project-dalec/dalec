package dalec

import (
	"testing"

	"github.com/moby/buildkit/client/llb"
)

func TestCheckOutputGetSourceLocationProgrammaticSlices(t *testing.T) {
	t.Parallel()

	state := llb.Scratch()
	check := CheckOutput{
		Contains: []string{"foo"},
		Matches:  []string{"bar"},
	}

	for _, tc := range []struct {
		kind  string
		index int
	}{
		{kind: CheckOutputContainsKind, index: 0},
		{kind: CheckOutputContainsKind, index: 1},
		{kind: CheckOutputContainsKind, index: -1},
		{kind: CheckOutputMatchesKind, index: 0},
		{kind: CheckOutputMatchesKind, index: 1},
		{kind: CheckOutputMatchesKind, index: -1},
	} {
		if loc := check.GetSourceLocation(state, tc.kind, tc.index); loc != nil {
			t.Fatalf("expected nil source location for %s index %d, got %#v", tc.kind, tc.index, loc)
		}
	}
}
