package test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/project-dalec/dalec"
)

func TestFrontendCoverageExportedOnSolveError(t *testing.T) {
	ctx := startTestSpan(baseCtx, t)
	covDir := t.TempDir()
	t.Setenv("DALEC_FRONTEND_GOCOVERDIR", covDir)

	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
		req := newSolveRequest(
			withSpec(ctx, t, &dalec.Spec{
				Name:     "frontend-coverage-error",
				Version:  "0.0.1",
				Revision: "1",
			}),
			withBuildTarget("does-not-exist"),
		)

		_, err := gwc.Solve(ctx, req)
		const expect = "no such handler for target"
		if err == nil || !strings.Contains(err.Error(), expect) {
			t.Fatalf("expected error containing %q, got %v", expect, err)
		}

		assertNonEmptyGlob(t, filepath.Join(covDir, "covmeta.*"))
		assertNonEmptyGlob(t, filepath.Join(covDir, "covcounters.*"))
		assertNonZeroFrontendCoverageProfile(t, covDir)
	})
}

func assertNonEmptyGlob(t *testing.T, pattern string) {
	t.Helper()

	matches, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("expected nil glob error for %q, got %v", pattern, err)
	}
	if len(matches) == 0 {
		t.Fatalf("expected at least one file matching %q", pattern)
	}

	info, err := os.Stat(matches[0])
	if err != nil {
		t.Fatalf("expected stat to succeed for %q, got %v", matches[0], err)
	}
	if info.Size() == 0 {
		t.Fatalf("expected %q to be non-empty", matches[0])
	}
}

func assertNonZeroFrontendCoverageProfile(t *testing.T, covDir string) {
	t.Helper()

	profilePath := filepath.Join(t.TempDir(), "frontend.out")
	cmd := exec.Command("go", "tool", "covdata", "textfmt", "-i="+covDir, "-o="+profilePath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected covdata textfmt to succeed, got %v\n%s", err, out)
	}

	profile, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("expected frontend profile to be readable, got %v", err)
	}

	sawBlock := false
	for _, line := range strings.Split(strings.TrimSpace(string(profile)), "\n") {
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}

		sawBlock = true
		fields := strings.Fields(line)
		if len(fields) != 3 {
			t.Fatalf("expected coverage line to have 3 fields, got %q", line)
		}

		count, err := strconv.ParseUint(fields[2], 10, 64)
		if err != nil {
			t.Fatalf("expected numeric coverage count in %q, got %v", line, err)
		}
		if count > 0 {
			return
		}
	}

	if !sawBlock {
		t.Fatalf("expected frontend coverage profile %q to contain blocks, got:\n%s", profilePath, profile)
	}
	t.Fatalf("expected frontend coverage profile %q to contain at least one non-zero block, got:\n%s", profilePath, profile)
}
