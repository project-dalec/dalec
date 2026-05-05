package specgen

import (
	"fmt"
	"strings"

	"github.com/project-dalec/dalec"
)

type baselineAdapter interface {
	ID() string
	Supports(f *RepoFacts, plan *SpecPlan) bool
	ConfigureSource(spec *dalec.Spec, a *Analysis, plan *SpecPlan, warnings *[]string)
	EmitBuild(spec *dalec.Spec, a *Analysis, plan *SpecPlan, warnings *[]string) (map[string]string, string)
}

func selectBaselineAdapter(f *RepoFacts, plan *SpecPlan) baselineAdapter {
	style := strings.TrimSpace(planBuildStyle(plan))
	if style != "" {
		switch {
		case strings.HasPrefix(style, "go"):
			return goBaselineAdapter{}
		case strings.HasPrefix(style, "rust"):
			return rustBaselineAdapter{}
		case strings.HasPrefix(style, "node"):
			return nodeBaselineAdapter{}
		case strings.HasPrefix(style, "python"):
			return pythonBaselineAdapter{}
		default:
			return genericBaselineAdapter{}
		}
	}

	adapters := []baselineAdapter{
		goBaselineAdapter{},
		rustBaselineAdapter{},
		nodeBaselineAdapter{},
		pythonBaselineAdapter{},
		genericBaselineAdapter{},
	}
	for _, adapter := range adapters {
		if adapter.Supports(f, plan) {
			return adapter
		}
	}
	return genericBaselineAdapter{}
}

type goBaselineAdapter struct{}
type rustBaselineAdapter struct{}
type nodeBaselineAdapter struct{}
type pythonBaselineAdapter struct{}
type genericBaselineAdapter struct{}

func (goBaselineAdapter) ID() string      { return "go" }
func (rustBaselineAdapter) ID() string    { return "rust" }
func (nodeBaselineAdapter) ID() string    { return "node" }
func (pythonBaselineAdapter) ID() string  { return "python" }
func (genericBaselineAdapter) ID() string { return "generic" }

func (goBaselineAdapter) Supports(f *RepoFacts, plan *SpecPlan) bool {
	return f != nil && (f.PrimaryType == "go" || strings.HasPrefix(strings.TrimSpace(planBuildStyle(plan)), "go"))
}
func (rustBaselineAdapter) Supports(f *RepoFacts, plan *SpecPlan) bool {
	return f != nil && (f.PrimaryType == "rust" || strings.HasPrefix(strings.TrimSpace(planBuildStyle(plan)), "rust"))
}
func (nodeBaselineAdapter) Supports(f *RepoFacts, plan *SpecPlan) bool {
	return f != nil && (f.PrimaryType == "node" || strings.HasPrefix(strings.TrimSpace(planBuildStyle(plan)), "node"))
}
func (pythonBaselineAdapter) Supports(f *RepoFacts, plan *SpecPlan) bool {
	return f != nil && (f.PrimaryType == "python" || strings.HasPrefix(strings.TrimSpace(planBuildStyle(plan)), "python"))
}
func (genericBaselineAdapter) Supports(f *RepoFacts, plan *SpecPlan) bool { return true }

func (goBaselineAdapter) ConfigureSource(spec *dalec.Spec, a *Analysis, plan *SpecPlan, warnings *[]string) {
	configureSuggestedSourceGenerator(spec, a, warnings)
}
func (rustBaselineAdapter) ConfigureSource(spec *dalec.Spec, a *Analysis, plan *SpecPlan, warnings *[]string) {
	configureSuggestedSourceGenerator(spec, a, warnings)
}
func (nodeBaselineAdapter) ConfigureSource(spec *dalec.Spec, a *Analysis, plan *SpecPlan, warnings *[]string) {
	configureSuggestedSourceGenerator(spec, a, warnings)
}
func (pythonBaselineAdapter) ConfigureSource(spec *dalec.Spec, a *Analysis, plan *SpecPlan, warnings *[]string) {
	configureSuggestedSourceGenerator(spec, a, warnings)
}
func (genericBaselineAdapter) ConfigureSource(spec *dalec.Spec, a *Analysis, plan *SpecPlan, warnings *[]string) {
	if spec == nil {
		return
	}
	src := spec.Sources["src"]
	src.Generate = nil
	spec.Sources["src"] = src
}

func (goBaselineAdapter) EmitBuild(spec *dalec.Spec, a *Analysis, plan *SpecPlan, warnings *[]string) (map[string]string, string) {
	env := map[string]string{}
	return env, buildGoByPlan(spec, a, plan, warnings, env)
}
func (rustBaselineAdapter) EmitBuild(spec *dalec.Spec, a *Analysis, plan *SpecPlan, warnings *[]string) (map[string]string, string) {
	env := map[string]string{}
	return env, buildRustByPlan(spec, a, plan, warnings, env)
}
func (nodeBaselineAdapter) EmitBuild(spec *dalec.Spec, a *Analysis, plan *SpecPlan, warnings *[]string) (map[string]string, string) {
	env := map[string]string{}
	return env, buildNodeByPlan(spec, a, plan, warnings, env)
}
func (pythonBaselineAdapter) EmitBuild(spec *dalec.Spec, a *Analysis, plan *SpecPlan, warnings *[]string) (map[string]string, string) {
	env := map[string]string{}
	return env, buildPythonByPlan(spec, a, plan, warnings, env)
}
func (genericBaselineAdapter) EmitBuild(spec *dalec.Spec, a *Analysis, plan *SpecPlan, warnings *[]string) (map[string]string, string) {
	return map[string]string{}, genericBuildCommand(analysisFacts(a))
}

func configureSuggestedSourceGenerator(spec *dalec.Spec, a *Analysis, warnings *[]string) {
	if spec == nil {
		return
	}

	src := spec.Sources["src"]
	src.Generate = nil

	f := analysisFacts(a)
	if f == nil {
		spec.Sources["src"] = src
		return
	}

	switch strings.TrimSpace(f.SuggestedSourceGenerator) {
	case "gomod":
		if f.SuggestedSourceGeneratorSafe {
			src.Generate = []*dalec.SourceGenerator{{Gomod: &dalec.GeneratorGomod{}}}
		} else if strings.TrimSpace(f.SuggestedSourceGeneratorReason) != "" {
			*warnings = append(*warnings, fmt.Sprintf("skipping optional gomod source generator: %s", strings.TrimSpace(f.SuggestedSourceGeneratorReason)))
		}
	case "cargohome":
		src.Generate = []*dalec.SourceGenerator{{Cargohome: &dalec.GeneratorCargohome{}}}
	case "node-mod":
		src.Generate = []*dalec.SourceGenerator{{NodeMod: &dalec.GeneratorNodeMod{}}}
	case "pip":
		src.Generate = []*dalec.SourceGenerator{{Pip: &dalec.GeneratorPip{}}}
	}

	spec.Sources["src"] = src
}

func planBuildStyle(plan *SpecPlan) string {
	if plan == nil {
		return ""
	}
	return strings.TrimSpace(plan.BuildStyle)
}

func analysisFacts(a *Analysis) *RepoFacts {
	if a == nil {
		return nil
	}
	return a.Facts
}
