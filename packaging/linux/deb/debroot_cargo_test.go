package deb

import (
	"strings"
	"testing"

	"github.com/project-dalec/dalec"
)

func TestCreateBuildScriptSetsCargoOfflineMode(t *testing.T) {
	t.Parallel()

	spec := &dalec.Spec{
		Sources: map[string]dalec.Source{
			"src": {Generate: []*dalec.SourceGenerator{{Cargohome: &dalec.GeneratorCargohome{}}}},
		},
		Build: dalec.ArtifactBuild{Steps: dalec.BuildStepList{{Command: "cargo build"}}},
	}

	script := string(createBuildScript(spec, nil))
	if !strings.Contains(script, "export CARGO_HOME=\"$(pwd)/"+cargohomeName+"\"") {
		t.Fatalf("expected CARGO_HOME setup, got:\n%s", script)
	}
	if !strings.Contains(script, "export CARGO_NET_OFFLINE=true") {
		t.Fatalf("expected Cargo offline mode, got:\n%s", script)
	}
}
