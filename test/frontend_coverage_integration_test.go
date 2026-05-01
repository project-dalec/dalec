package test

import (
	"bufio"
	"bytes"
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
		assertFrontendCoverageHasCounters(t, covDir)
	})
}

func assertFrontendCoverageHasCounters(t *testing.T, covDir string) {
	t.Helper()

	profile := filepath.Join(t.TempDir(), "frontend.out")
	cmd := exec.Command("go", "tool", "covdata", "textfmt", "-i", covDir, "-o", profile)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("expected covdata textfmt to succeed, got %v: %s", err, stderr.String())
	}

	f, err := os.Open(profile)
	if err != nil {
		t.Fatalf("expected coverage profile to open, got %v", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 3 || fields[0] == "mode:" {
			continue
		}

		count, err := strconv.ParseUint(fields[2], 10, 64)
		if err != nil {
			t.Fatalf("expected coverage count to parse from %q, got %v", scanner.Text(), err)
		}
		if count > 0 {
			return
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("expected coverage profile scan to succeed, got %v", err)
	}

	t.Fatal("expected frontend coverage profile to contain at least one non-zero counter")
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
