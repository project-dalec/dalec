package specgen

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/project-dalec/dalec"
	"gopkg.in/yaml.v3"
)

type Options struct {
	RepoDir     string
	OutFile     string
	SourceMode  string
	ForcedType  string
	SyntaxImage string
	RepoURL     string

	Intent        IntentMode
	TargetFamily  TargetFamily
	MainComponent string
	TestMode      TestMode
	EmitTargets   bool

	// Baseline-focused options.
	BundleOut       string
	PreferGitSource bool
	EmitArgs        bool
	RichPlan        bool

	// User-supplied build intent.
	// These fields are applied before heuristic ranking so repo discovery answers
	// "what is available" while user intent answers "what should be built".
	BinaryNames []string
	PackageName string
	BinaryName  string
	BuildStyle  string
	BuildTarget string
	Entrypoint  string
	Command     string

	// ExtraBuildDeps is a list of extra build-time package dependencies to
	// include in spec.dependencies.build, e.g. ["libssl-dev", "zlib1g-dev"].
	ExtraBuildDeps []string

	// ExtraRuntimeDeps is a list of extra runtime package dependencies to
	// include in spec.dependencies.runtime.
	ExtraRuntimeDeps []string

	// VersionVarPath is the fully-qualified Go ldflags version variable path,
	// e.g. "main.version" or "github.com/foo/bar/cmd.Version".
	// When set, the baseline emits -ldflags "-X <path>=${VERSION}".
	VersionVarPath string

	// CGOEnabled explicitly overrides the CGO_ENABLED build environment variable.
	// nil means auto (defaults to off for most Go builds for reproducibility).
	CGOEnabled *bool

	// UserHints is free-text guidance forwarded to the AI refinement stage.
	// Ignored by the deterministic baseline generator.
	UserHints string

	// Legacy AI fields kept temporarily as no-ops so callers do not break
	// while we refactor the rest of the package file-by-file.
	BaselineOnly bool
	AIStage      string
	UseAI        bool
	AIOnUnknown  bool
	AIOnWeak     bool
	LLMCmd       string
	MaxFiles     int
	MaxBytes     int
}

type Result struct {
	YAML         []byte
	DetectedType string
	Warnings     []string
	Spec         *dalec.Spec
	Analysis     *Analysis
	Plan         *SpecPlan
	Unresolved   []UnresolvedItem
	Bundle       *BaselineBundle
}

// BaselineBundle is intentionally minimal in this file so specgen.go can move
// to a baseline-first flow immediately. We will enrich this further when we
// update types.go / analysis.go / outputplan.go.
type BaselineBundle struct {
	SchemaVersion int              `json:"schema_version"`
	RepoDir       string           `json:"repo_dir"`
	DetectedType  string           `json:"detected_type"`
	Warnings      []string         `json:"warnings,omitempty"`
	Analysis      *Analysis        `json:"analysis,omitempty"`
	Plan          *SpecPlan        `json:"plan,omitempty"`
	Unresolved    []UnresolvedItem `json:"unresolved,omitempty"`
	Spec          *dalec.Spec      `json:"spec,omitempty"`
}

// Generate is kept for compatibility with existing callers.
// Internally it now runs the deterministic baseline-only pipeline.
func Generate(ctx context.Context, opts Options) (*Result, error) {
	return GenerateBaseline(ctx, opts)
}

func GenerateBaseline(ctx context.Context, opts Options) (*Result, error) {
	opts = normalizeBaselineOptions(opts)

	repoDir, err := filepath.Abs(opts.RepoDir)
	if err != nil {
		return nil, err
	}
	opts.RepoDir = repoDir

	facts, warnings, err := DetectRepo(repoDir, opts.ForcedType)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(opts.RepoURL) != "" && strings.TrimSpace(facts.GitRemoteURL) == "" {
		facts.GitRemoteURL = strings.TrimSpace(opts.RepoURL)
	}
	if opts.PreferGitSource && strings.EqualFold(strings.TrimSpace(opts.SourceMode), "context") &&
		strings.TrimSpace(facts.GitRemoteURL) != "" && strings.TrimSpace(facts.GitCommit) != "" {
		opts.SourceMode = "git"
	}

	analysis, analysisWarnings := AnalyzeRepo(facts)
	warnings = append(warnings, analysisWarnings...)

	plan := deterministicPlan(opts, facts, analysis)
	if plan == nil {
		return nil, fmt.Errorf("deterministic planning returned nil")
	}
	if err := validatePlan(plan); err != nil {
		return nil, err
	}

	spec, buildWarnings, err := BuildSpec(ctx, analysis, plan, opts.SourceMode)
	if err != nil {
		return nil, err
	}
	warnings = append(warnings, buildWarnings...)

	if err := validateFinalSpec(spec, plan); err != nil {
		return nil, fmt.Errorf("generated spec failed validation: %w", err)
	}

	specGaps := FindSpecGaps(analysis, spec)

	unresolved := append([]UnresolvedItem(nil), analysis.Unresolved...)
	unresolved = append(unresolved, specGaps...)
	unresolved = dedupeUnresolved(unresolved)

	warnings = dedupeWarnings(warnings)

	var buf bytes.Buffer
	if strings.TrimSpace(opts.SyntaxImage) != "" {
		fmt.Fprintf(&buf, "# syntax=%s\n", strings.TrimSpace(opts.SyntaxImage))
	}

	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(spec); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}

	bundle := &BaselineBundle{
		SchemaVersion: 1,
		RepoDir:       repoDir,
		DetectedType:  facts.PrimaryType,
		Warnings:      append([]string(nil), warnings...),
		Analysis:      analysis,
		Plan:          plan,
		Unresolved:    append([]UnresolvedItem(nil), unresolved...),
		Spec:          spec,
	}

	return &Result{
		YAML:         buf.Bytes(),
		DetectedType: facts.PrimaryType,
		Warnings:     warnings,
		Spec:         spec,
		Analysis:     analysis,
		Plan:         plan,
		Unresolved:   unresolved,
		Bundle:       bundle,
	}, nil
}

func normalizeBaselineOptions(opts Options) Options {
	if strings.TrimSpace(opts.RepoDir) == "" {
		opts.RepoDir = "."
	}
	if strings.TrimSpace(opts.SyntaxImage) == "" {
		opts.SyntaxImage = "ghcr.io/project-dalec/dalec/frontend:latest"
	}
	if strings.TrimSpace(opts.SourceMode) == "" {
		opts.SourceMode = "context"
	}
	if opts.Intent == "" {
		opts.Intent = IntentAuto
	}
	if opts.TargetFamily == "" {
		opts.TargetFamily = TargetFamilyAuto
	}
	if opts.TestMode == "" {
		opts.TestMode = TestAuto
	}

	return opts
}
