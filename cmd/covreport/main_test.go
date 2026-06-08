package main

import (
	"strings"
	"testing"
)

const sampleFuncOutput = `github.com/project-dalec/dalec/a.go:10:	AlphaFunc		100.0%
github.com/project-dalec/dalec/a.go:20:	BetaFunc		0.0%
github.com/project-dalec/dalec/b.go:5:	GammaFunc		50.0%
github.com/project-dalec/dalec/b.go:30:	DeltaFunc		50.0%
github.com/project-dalec/dalec/c.go:1:	EpsilonFunc		99.9%
total:							(statements)		70.0%
`

func TestParse(t *testing.T) {
	got := parse(sampleFuncOutput)
	if len(got) != 5 {
		t.Fatalf("expected 5 entries (total: excluded), got %d: %#v", len(got), got)
	}

	first := got[0]
	if first.location != "github.com/project-dalec/dalec/a.go:10" {
		t.Fatalf("unexpected location: %q", first.location)
	}
	if first.function != "AlphaFunc" {
		t.Fatalf("unexpected function: %q", first.function)
	}
	if first.percent != 100.0 {
		t.Fatalf("unexpected percent: %v", first.percent)
	}
}

func TestParseSkipsTotalAndMalformed(t *testing.T) {
	in := sampleFuncOutput + "this is not a coverage line\nfoo.go:1:\tBar\tnotapercent\n\n"
	got := parse(in)
	for _, e := range got {
		if e.location == "total" || e.location == "total:" {
			t.Fatalf("total line should be excluded, got %#v", e)
		}
		if e.function == "Bar" {
			t.Fatalf("malformed percent line should be excluded, got %#v", e)
		}
	}
	if len(got) != 5 {
		t.Fatalf("expected 5 valid entries, got %d", len(got))
	}
}

func TestMissingFiltersAndSorts(t *testing.T) {
	got := missing(parse(sampleFuncOutput))

	if len(got) != 4 {
		t.Fatalf("expected 4 entries below 100%%, got %d: %#v", len(got), got)
	}

	// Ascending by percent: 0.0 first, then the two 50.0 (tie-broken by
	// location: b.go:30 < b.go:5 lexically), then 99.9.
	wantOrder := []string{"BetaFunc", "DeltaFunc", "GammaFunc", "EpsilonFunc"}
	for i, want := range wantOrder {
		if got[i].function != want {
			t.Fatalf("position %d: expected %q, got %q (full: %#v)", i, want, got[i].function, got)
		}
	}
}

func TestRenderTopCap(t *testing.T) {
	var sb strings.Builder
	if err := render(&sb, sampleFuncOutput, 2); err != nil {
		t.Fatalf("render error: %v", err)
	}
	out := sb.String()

	if !strings.Contains(out, "## Missing coverage") {
		t.Fatalf("missing heading:\n%s", out)
	}
	if !strings.Contains(out, "2 of 4 functions below 100% coverage") {
		t.Fatalf("expected capped summary, got:\n%s", out)
	}
	if !strings.Contains(out, "| 0.0% | BetaFunc |") {
		t.Fatalf("expected BetaFunc row:\n%s", out)
	}
	if strings.Contains(out, "EpsilonFunc") {
		t.Fatalf("EpsilonFunc should be capped out:\n%s", out)
	}
}

func TestRenderShowsAllWhenUnderCap(t *testing.T) {
	var sb strings.Builder
	if err := render(&sb, sampleFuncOutput, 20); err != nil {
		t.Fatalf("render error: %v", err)
	}
	out := sb.String()
	if !strings.Contains(out, "4 least-covered functions (below 100%)") {
		t.Fatalf("expected full-list summary, got:\n%s", out)
	}
	if !strings.Contains(out, "EpsilonFunc") {
		t.Fatalf("expected EpsilonFunc present:\n%s", out)
	}
}

func TestRenderFullyCovered(t *testing.T) {
	in := `github.com/project-dalec/dalec/a.go:10:	AlphaFunc		100.0%
total:							(statements)		100.0%
`
	var sb strings.Builder
	if err := render(&sb, in, 20); err != nil {
		t.Fatalf("render error: %v", err)
	}
	out := sb.String()
	if !strings.Contains(out, "All tracked functions are fully covered.") {
		t.Fatalf("expected fully-covered note, got:\n%s", out)
	}
	if strings.Contains(out, "<details>") {
		t.Fatalf("did not expect a table for fully-covered input:\n%s", out)
	}
}

func TestRenderEmptyInput(t *testing.T) {
	var sb strings.Builder
	if err := render(&sb, "", 20); err != nil {
		t.Fatalf("render error: %v", err)
	}
	if !strings.Contains(sb.String(), "All tracked functions are fully covered.") {
		t.Fatalf("expected fully-covered note for empty input, got:\n%s", sb.String())
	}
}

func TestParseFlags(t *testing.T) {
	cfg := parseFlags([]string{"-input", "cov.txt", "-top", "5"})
	if cfg.input != "cov.txt" || cfg.top != 5 {
		t.Fatalf("unexpected config: %#v", cfg)
	}

	cfg = parseFlags([]string{"-input=cov.txt", "-top=7"})
	if cfg.input != "cov.txt" || cfg.top != 7 {
		t.Fatalf("unexpected config (equals form): %#v", cfg)
	}

	cfg = parseFlags(nil)
	if cfg.top != 20 {
		t.Fatalf("expected default top=20, got %d", cfg.top)
	}
}
