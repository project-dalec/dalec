package specgen

import (
	"fmt"
	"strings"

	"github.com/project-dalec/dalec"
)

func validateSpecMinimal(spec *dalec.Spec) error {
	return validateSpecStructural(spec)
}

func validateSpecStructural(spec *dalec.Spec) error {
	if spec == nil {
		return fmt.Errorf("spec is nil")
	}
	if strings.TrimSpace(spec.Name) == "" {
		return fmt.Errorf("spec name is empty")
	}
	if len(spec.Sources) == 0 {
		return fmt.Errorf("spec has no sources")
	}

	for name, src := range spec.Sources {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("spec has a source with empty name")
		}
		if src.Context == nil && src.Path == "" && len(src.Generate) == 0 {
			return fmt.Errorf("source %q appears empty", name)
		}
	}

	for i, s := range spec.Build.Steps {
		if strings.TrimSpace(s.Command) == "" {
			return fmt.Errorf("build step %d has empty command", i)
		}
	}

	for _, test := range spec.Tests {
		if strings.TrimSpace(test.Name) == "" {
			return fmt.Errorf("test name is empty")
		}
		for i, step := range test.Steps {
			if strings.TrimSpace(step.Command) == "" {
				return fmt.Errorf("test %q step %d has empty command", test.Name, i)
			}
		}
	}

	return nil
}

func validateSpecSemantic(spec *dalec.Spec, plan *SpecPlan) error {
	if spec == nil {
		return fmt.Errorf("spec is nil")
	}

	if strings.TrimSpace(spec.Version) == "" {
		return fmt.Errorf("spec version is empty")
	}
	if strings.TrimSpace(spec.Revision) == "" {
		return fmt.Errorf("spec revision is empty")
	}

	for _, step := range spec.Build.Steps {
		if looksDangerous(step.Command) {
			return fmt.Errorf("unsafe build command detected")
		}
	}

	for _, test := range spec.Tests {
		for _, step := range test.Steps {
			if looksDangerous(step.Command) {
				return fmt.Errorf("unsafe test command detected in %q", test.Name)
			}
		}
	}

	if plan != nil && plan.Intent == IntentContainerOnly {
		if err := validateContainerOnlyShape(spec); err != nil {
			return err
		}
		return validateFileBasedTests(spec)
	}

	if hasDeclaredBinaryArtifacts(spec) {
		if err := validateBinaryArtifacts(spec); err != nil {
			return err
		}
	}

	if err := validateGoStyleSmokeTests(spec); err != nil {
		return err
	}
	if err := validateFileBasedTests(spec); err != nil {
		return err
	}

	if strings.TrimSpace(spec.Name) == "" || strings.TrimSpace(spec.Version) == "" {
		return fmt.Errorf("invalid spec metadata")
	}

	return nil
}

func validatePlan(plan *SpecPlan) error {
	if plan == nil {
		return fmt.Errorf("plan is nil")
	}
	if plan.Intent == "" {
		return fmt.Errorf("plan intent is empty")
	}
	if plan.TargetFamily == "" {
		return fmt.Errorf("plan target family is empty")
	}
	if plan.Intent == IntentWindowsCross && plan.TargetFamily != TargetFamilyWindows {
		return fmt.Errorf("windowscross intent requires windows target family")
	}
	if plan.Intent == IntentContainerOnly && strings.TrimSpace(plan.BuildStyle) == "" {
		return fmt.Errorf("container-only intent requires a build style decision, even if build emission is later empty")
	}
	if plan.UseTargets && len(plan.Routes) == 0 {
		return fmt.Errorf("plan uses targets but has no routes")
	}
	if strings.TrimSpace(plan.PackageName) == "" {
		return fmt.Errorf("plan package name is empty")
	}
	if strings.TrimSpace(plan.MainComponent) == "" && plan.Intent != IntentContainerOnly {
		return fmt.Errorf("plan main component is empty")
	}
	return nil
}

func validateFinalSpec(spec *dalec.Spec, plan *SpecPlan) error {
	if err := validateSpecStructural(spec); err != nil {
		return err
	}
	if err := validateSpecSemantic(spec, plan); err != nil {
		return err
	}
	return nil
}

func validateContainerOnlyShape(spec *dalec.Spec) error {
	if spec == nil {
		return nil
	}

	// Container-only baseline is allowed to have no build steps.
	for i, step := range spec.Build.Steps {
		if strings.TrimSpace(step.Command) == "" {
			return fmt.Errorf("container-only spec has empty build step %d", i)
		}
	}

	return nil
}

func hasDeclaredBinaryArtifacts(spec *dalec.Spec) bool {
	if spec == nil {
		return false
	}
	return len(spec.Artifacts.Binaries) > 0
}

func validateBinaryArtifacts(spec *dalec.Spec) error {
	if spec == nil || len(spec.Artifacts.Binaries) == 0 {
		return nil
	}

	for p := range spec.Artifacts.Binaries {
		pp := normalizeArtifactPath(strings.TrimSpace(p))
		if pp == "" {
			return fmt.Errorf("binary artifact path is empty")
		}
		if strings.Contains(strings.ToLower(pp), "<unknown>") {
			return fmt.Errorf("binary artifact path contains placeholder value")
		}
		if !binaryArtifactPathAllowed(pp, spec) {
			return fmt.Errorf("binary artifact path %q does not look consistent with the baseline build layout", pp)
		}
	}

	return nil
}

func binaryArtifactPathAllowed(path string, spec *dalec.Spec) bool {
	path = normalizeArtifactPath(path)
	low := strings.ToLower(path)

	// Preferred normalized native layout.
	if strings.HasPrefix(low, "src/bin/") {
		return true
	}

	// Windows-cross binary in normalized layout.
	if strings.HasPrefix(low, "src/bin/") && strings.HasSuffix(low, ".exe") {
		return true
	}

	// Common cargo layout.
	if strings.HasPrefix(low, "src/target/release/") {
		return true
	}
	if strings.HasPrefix(low, "src/target/release/") && strings.HasSuffix(low, ".exe") {
		return true
	}

	// Python dist-style fallback.
	if strings.HasPrefix(low, "src/dist/") {
		return true
	}

	// Legacy / make-driven fallback.
	if strings.HasPrefix(low, "src/.out/") {
		return true
	}

	// If the build explicitly writes the same relative output path, allow it.
	if buildStepsReferenceArtifact(path, spec) {
		return true
	}

	return false
}

func buildStepsReferenceArtifact(artifactPath string, spec *dalec.Spec) bool {
	if spec == nil {
		return false
	}

	artifactPath = normalizeArtifactPath(artifactPath)
	trimmed := strings.TrimPrefix(artifactPath, "src/")
	trimmed = strings.TrimPrefix(trimmed, "./")
	trimmedLow := strings.ToLower(trimmed)
	fullLow := strings.ToLower(artifactPath)

	for _, step := range spec.Build.Steps {
		cmd := strings.ToLower(strings.TrimSpace(step.Command))
		if cmd == "" {
			continue
		}

		if strings.Contains(cmd, fullLow) {
			return true
		}
		if trimmedLow != "" && strings.Contains(cmd, trimmedLow) {
			return true
		}
	}

	return false
}

func validateGoStyleSmokeTests(spec *dalec.Spec) error {
	if spec == nil || len(spec.Tests) == 0 {
		return nil
	}

	for _, test := range spec.Tests {
		for _, step := range test.Steps {
			cmd := strings.TrimSpace(step.Command)
			if strings.HasPrefix(cmd, "./bin/") && strings.TrimSpace(test.Dir) == "" {
				return fmt.Errorf("test %q executes ./bin/... but has no dir set", test.Name)
			}
			if strings.HasPrefix(cmd, "./bin/") && strings.HasSuffix(cmd, ".exe") && strings.TrimSpace(test.Dir) == "" {
				return fmt.Errorf("test %q executes ./bin/*.exe but has no dir set", test.Name)
			}
		}
	}

	return nil
}

func validateFileBasedTests(spec *dalec.Spec) error {
	if spec == nil || len(spec.Tests) == 0 {
		return nil
	}

	for _, test := range spec.Tests {
		for path := range test.Files {
			p := strings.TrimSpace(path)
			if p == "" {
				return fmt.Errorf("test %q has empty file assertion path", test.Name)
			}
			if !strings.HasPrefix(p, "/") {
				return fmt.Errorf("test %q has non-absolute file assertion path %q", test.Name, p)
			}
		}
	}

	return nil
}
