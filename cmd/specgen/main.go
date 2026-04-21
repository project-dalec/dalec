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

		// User-intent flags — control what to build without needing AI refinement.
		binaryNames = flag.String(
			"binaries", "",
			"Comma-separated explicit binary names for multi-binary repos, e.g. myctl,myagent.\n"+
				"Overrides component-selection scoring. First name becomes the primary component.",
		)
		packageName    = flag.String("package-name", "", "Explicit package name to emit in the baseline spec")
		binaryName     = flag.String("binary-name", "", "Explicit primary installed binary name when it differs from the package name")
		buildStyle     = flag.String("build-style", "", "Explicit baseline build style, e.g. go-make, go-simple, rust-workspace")
		buildTarget    = flag.String("build-target", "", "Explicit primary source build target, e.g. ./cmd/operator or ./cmd/gh")
		entrypoint     = flag.String("entrypoint", "", "Explicit runtime entrypoint for image/tests")
		cmdFlag        = flag.String("cmd", "", "Explicit runtime command/arguments for image/tests")
		extraBuildDeps = flag.String(
			"extra-build-deps", "",
			"Comma-separated extra build-time package dependencies to inject into\n"+
				"spec.dependencies.build, e.g. libssl-dev,zlib1g-dev,pkg-config.",
		)
		extraRuntimeDeps = flag.String(
			"extra-runtime-deps", "",
			"Comma-separated extra runtime package dependencies to inject into\n"+
				"spec.dependencies.runtime.",
		)
		versionVarPath = flag.String(
			"version-var", "",
			"Fully-qualified Go ldflags version variable path used to inject the\n"+
				"build-time version string, e.g. main.version or\n"+
				"github.com/foo/bar/cmd.Version.\n"+
				"When set the baseline emits: go build -ldflags \"-X <path>=${VERSION}\".",
		)
		cgoEnabled = flag.String(
			"cgo", "auto",
			"CGO mode for Go builds: auto|true|false.\n"+
				"Default: auto (baseline keeps CGO disabled unless explicitly enabled).",
		)
		userHints = flag.String(
			"hints", "",
			"Free-text hints forwarded to the AI refinement stage.\n"+
				"Ignored by the deterministic baseline generator.",
		)
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

	cgoPtr, err := parseOptionalBool(*cgoEnabled)
	if err != nil {
		fmt.Fprintf(
			os.Stderr,
			"specgen error: invalid --cgo value %q: %v\n",
			*cgoEnabled, err,
		)
		os.Exit(1)
	}

	opts := specgen.Options{
		RepoDir:          *repo,
		OutFile:          *out,
		SourceMode:       *source,
		ForcedType:       *force,
		SyntaxImage:      "ghcr.io/project-dalec/dalec/frontend:latest",
		Intent:           resolvedIntent,
		TargetFamily:     specgen.ParseTargetFamily(*targetFamily),
		MainComponent:    strings.TrimSpace(*mainComp),
		TestMode:         specgen.ParseTestMode(*withTests),
		EmitTargets:      *emitTargets,
		BundleOut:        strings.TrimSpace(*bundleOut),
		PreferGitSource:  *preferGit,
		EmitArgs:         *emitArgs,
		RichPlan:         *richPlan,
		BinaryNames:      splitTrimmed(*binaryNames),
		PackageName:      strings.TrimSpace(*packageName),
		BinaryName:       strings.TrimSpace(*binaryName),
		BuildStyle:       strings.TrimSpace(*buildStyle),
		BuildTarget:      strings.TrimSpace(*buildTarget),
		Entrypoint:       strings.TrimSpace(*entrypoint),
		Command:          strings.TrimSpace(*cmdFlag),
		ExtraBuildDeps:   splitTrimmed(*extraBuildDeps),
		ExtraRuntimeDeps: splitTrimmed(*extraRuntimeDeps),
		VersionVarPath:   strings.TrimSpace(*versionVarPath),
		CGOEnabled:       cgoPtr,
		UserHints:        strings.TrimSpace(*userHints),
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

// splitTrimmed splits a comma-separated string into a slice, trimming each
// element and dropping empty entries.
func splitTrimmed(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseOptionalBool(s string) (*bool, error) {
	switch strings.TrimSpace(strings.ToLower(s)) {
	case "", "auto":
		return nil, nil
	case "1", "t", "true", "yes", "on":
		v := true
		return &v, nil
	case "0", "f", "false", "no", "off":
		v := false
		return &v, nil
	default:
		return nil, fmt.Errorf("expected auto|true|false")
	}
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
