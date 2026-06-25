package testenv

import (
	"context"
	"slices"
	"testing"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/opencontainers/go-digest"
)

// withDeterminismCheck wraps a client so that every successful frontend solve is
// re-run and its generated LLB compared against the first run. It is intended to
// be the outermost wrapper so it observes the requests tests actually issue and
// re-issues them through the full frontend chain.
func withDeterminismCheck(c gwclient.Client, t *testing.T) gwclient.Client {
	return withCurrentFrontend(c, &determinismChecker{Client: c, t: t})
}

// determinismChecker re-runs the frontend for a request and asserts it produces
// identical LLB.
//
// BuildKit executes the gateway frontend container on every solve (the frontend
// process itself is not cached), so re-solving genuinely re-runs the frontend's
// LLB-generation code. The re-solve forces Evaluate=false so the emitted refs
// are not built: only the frontend runs, which keeps the check cheap relative to
// the original (often Evaluate=true) build while still exercising LLB
// generation.
//
// Only op-definition digests are compared. Legitimate per-run values such as
// progress-group IDs and the image "Created" timestamp live in op metadata and
// the image config, not in the op definitions, so comparing definition digests
// avoids false positives while still catching non-deterministic op generation
// (e.g. unsorted map iteration).
type determinismChecker struct {
	gwclient.Client
	t *testing.T
}

func (c *determinismChecker) Solve(ctx context.Context, req gwclient.SolveRequest) (*gwclient.Result, error) {
	res, err := c.Client.Solve(ctx, req)
	if err != nil {
		return res, err
	}

	// A request carrying a pre-built definition goes straight to the buildkit
	// solver rather than running the dalec frontend, so there is no frontend
	// LLB generation to check for determinism.
	if req.Definition != nil {
		return res, nil
	}

	req.Evaluate = false
	res2, err := c.Client.Solve(ctx, req)
	if err != nil {
		c.t.Errorf("determinism: re-solving request failed: %+v", err)
		return res, nil
	}

	want := resultLLBDigests(ctx, c.t, res)
	got := resultLLBDigests(ctx, c.t, res2)

	if !slices.Equal(got, want) {
		c.t.Errorf("frontend produced non-deterministic LLB across solves:\nfirst:  %v\nsecond: %v", want, got)
	}

	return res, nil
}

// resultLLBDigests returns the sorted digests of every op definition across all
// refs in the result. Sorting makes the comparison independent of the
// (map-backed, non-deterministic) iteration order of multi-platform refs.
func resultLLBDigests(ctx context.Context, t *testing.T, res *gwclient.Result) []string {
	t.Helper()

	var digests []string
	if err := res.EachRef(func(ref gwclient.Reference) error {
		st, err := ref.ToState()
		if err != nil {
			return err
		}

		def, err := st.Marshal(ctx)
		if err != nil {
			return err
		}

		for _, dt := range def.Def {
			digests = append(digests, digest.FromBytes(dt).String())
		}

		return nil
	}); err != nil {
		t.Errorf("determinism: extracting LLB from result: %v", err)
	}

	slices.Sort(digests)
	return digests
}
