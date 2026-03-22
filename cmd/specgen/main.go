package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/project-dalec/dalec/internal/specgen"
)

func main() {
	var (
		repo   = flag.String("repo", ".", "Path to the repository to analyze")
		out    = flag.String("out", "dalec.yml", "Output path for generated Dalec spec")
		source = flag.String("source", "context", "Source mode: context|git")
		force  = flag.String("type", "", "Force repo type: go|rust|node|python (optional)")
		printY = flag.Bool("print", false, "Print YAML to stdout instead of writing a file")

		// New preferred control.
		outputProfile = flag.String(
			"output-profile",
			"auto",
			"Preferred output profile: auto|package|package+container|container|sysext|windowscross",
		)

		// Legacy controls kept for compatibility.
		intent       = flag.String("intent", "auto", "Legacy output intent: auto|package|package+container|container-only|sysext|windowscross")
		targetFamily = flag.String("target-family", "auto", "Target family: auto|rpm|deb|both|windows")
		mainComp     = flag.String("main-component", "", "Optional explicit primary binary/app/service name for complex repos")
		withTests    = flag.String("with-tests", "auto", "Test generation mode: auto|always|never")
		emitTargets  = flag.Bool("emit-targets", true, "Emit target-aware planning information in the baseline plan")

		bundleOut     = flag.String("bundle-out", "", "Optional path to write the full baseline bundle JSON")
		analysisOut   = flag.String("analysis-out", "", "Optional path to write analysis JSON")
		planOut       = flag.String("plan-out", "", "Optional path to write plan JSON")
		unresolvedOut = flag.String("unresolved-out", "", "Optional path to write unresolved-items JSON")

		preferGit = flag.Bool("prefer-git-source", true, "Prefer git source when repo metadata is available")
		emitArgs  = flag.Bool("emit-args", true, "Emit VERSION/REVISION-style baseline args when useful")
		richPlan  = flag.Bool("rich-plan", true, "Preserve alternatives, decisions, and richer baseline planning context")
		strict    = flag.Bool("strict", false, "Fail if the baseline contains high-severity unresolved items")
	)

	flag.Parse()

	profile := specgen.ParseOutputProfile(*outputProfile)
	legacyIntent := specgen.ParseIntentMode(*intent)

	// output-profile is the preferred top-level knob.
	resolvedIntent := legacyIntent
	if profile != specgen.OutputProfileAuto {
		profileIntent := specgen.IntentFromOutputProfile(profile)
		if legacyIntent != "" && legacyIntent != specgen.IntentAuto && legacyIntent != profileIntent {
			fmt.Fprintf(
				os.Stderr,
				"specgen error: conflicting flags: --output-profile=%s maps to intent=%s but --intent=%s was also supplied\n",
				profile,
				profileIntent,
				legacyIntent,
			)
			os.Exit(1)
		}
		resolvedIntent = profileIntent
	}
	if resolvedIntent == "" {
		resolvedIntent = specgen.IntentAuto
	}

	opts := specgen.Options{
		RepoDir:         *repo,
		OutFile:         *out,
		SourceMode:      *source,
		ForcedType:      *force,
		SyntaxImage:     "ghcr.io/project-dalec/dalec/frontend:latest",
		Intent:          resolvedIntent,
		TargetFamily:    specgen.ParseTargetFamily(*targetFamily),
		MainComponent:   strings.TrimSpace(*mainComp),
		TestMode:        specgen.ParseTestMode(*withTests),
		EmitTargets:     *emitTargets,
		BundleOut:       strings.TrimSpace(*bundleOut),
		PreferGitSource: *preferGit,
		EmitArgs:        *emitArgs,
		RichPlan:        *richPlan,
	}

	res, err := specgen.GenerateBaseline(context.Background(), opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "specgen error: %v\n", err)
		os.Exit(1)
	}

	for _, w := range res.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}

	if *strict {
		if err := failOnHighSeverityUnresolved(res.Unresolved); err != nil {
			fmt.Fprintf(os.Stderr, "strict validation failed: %v\n", err)
			os.Exit(1)
		}
	}

	if err := writeJSONIfRequested(opts.BundleOut, res.Bundle); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if err := writeJSONIfRequested(*analysisOut, res.Analysis); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if err := writeJSONIfRequested(*planOut, res.Plan); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if err := writeJSONIfRequested(*unresolvedOut, res.Unresolved); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	if *printY {
		if _, err := os.Stdout.Write(res.YAML); err != nil {
			fmt.Fprintf(os.Stderr, "write stdout: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if err := os.WriteFile(opts.OutFile, res.YAML, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", opts.OutFile, err)
		os.Exit(1)
	}

	intentStr := "unknown"
	targetFamilyStr := "unknown"
	if res.Plan != nil {
		intentStr = string(res.Plan.Intent)
		targetFamilyStr = string(res.Plan.TargetFamily)
	}

	fmt.Fprintf(
		os.Stderr,
		"wrote %s (detected=%s intent=%s target-family=%s)\n",
		opts.OutFile,
		res.DetectedType,
		intentStr,
		targetFamilyStr,
	)
}

func writeJSONIfRequested(path string, v any) error {
	path = strings.TrimSpace(path)
	if path == "" || v == nil {
		return nil
	}

	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func failOnHighSeverityUnresolved(items []specgen.UnresolvedItem) error {
	var high []string
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item.Severity), "high") {
			msg := strings.TrimSpace(item.Message)
			if msg == "" {
				msg = item.Code
			}
			if msg == "" {
				msg = "unspecified high-severity unresolved item"
			}
			high = append(high, msg)
		}
	}
	if len(high) == 0 {
		return nil
	}
	return fmt.Errorf(strings.Join(high, "; "))
}
