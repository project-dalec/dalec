package dalec

import (
	"context"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
	pb "github.com/moby/buildkit/solver/pb"
)

func TestGomodReplaceUnmarshalYAMLValid(t *testing.T) {
	var replace GomodReplace
	if err := yaml.Unmarshal([]byte("\"github.com/example/mod@v1.2.3:../mod\""), &replace); err != nil {
		t.Fatalf("unexpected error unmarshalling gomod replace: %v", err)
	}

	old, new, err := replace.parts()
	if err != nil {
		t.Fatalf("unexpected error retrieving parts: %v", err)
	}

	if old != "github.com/example/mod@v1.2.3" {
		t.Fatalf("unexpected old value: %s", old)
	}

	if new != "../mod" {
		t.Fatalf("unexpected new value: %s", new)
	}
}

func TestGomodReplaceUnmarshalYAMLInvalid(t *testing.T) {
	var replace GomodReplace
	if err := yaml.Unmarshal([]byte("\"github.com/example/mod\""), &replace); err == nil {
		t.Fatalf("expected error unmarshalling gomod replace without colon")
	}
}

func TestGomodRequireUnmarshalYAMLValid(t *testing.T) {
	var require GomodRequire
	if err := yaml.Unmarshal([]byte("\"github.com/example/mod:github.com/example/mod@v1.2.3\""), &require); err != nil {
		t.Fatalf("unexpected error unmarshalling gomod require: %v", err)
	}

	module, target, err := require.parts()
	if err != nil {
		t.Fatalf("unexpected error retrieving parts: %v", err)
	}

	if module != "github.com/example/mod" {
		t.Fatalf("unexpected module value: %s", module)
	}

	if target != "github.com/example/mod@v1.2.3" {
		t.Fatalf("unexpected target value: %s", target)
	}
}

func TestGomodRequireUnmarshalYAMLInvalid(t *testing.T) {
	var require GomodRequire
	if err := yaml.Unmarshal([]byte("\"github.com/example/mod@v1.2.3\""), &require); err == nil {
		t.Fatalf("expected error unmarshalling gomod require without colon")
	}
}

func TestGeneratorGomodProcessBuildArgsReplace(t *testing.T) {
	gen := &GeneratorGomod{
		Replace: []GomodReplace{
			normalizeGomodReplace("github.com/example/mod@${VERSION}", "../local/${VERSION}"),
		},
		Require: []GomodRequire{
			normalizeGomodRequire("github.com/example/mod", "github.com/example/mod@${VERSION}"),
		},
	}

	args := map[string]string{"VERSION": "v1.2.3"}
	if err := gen.processBuildArgs(args, func(string) bool { return false }); err != nil {
		t.Fatalf("unexpected error processing build args: %v", err)
	}

	old, new, err := gen.Replace[0].parts()
	if err != nil {
		t.Fatalf("unexpected error retrieving parsed parts: %v", err)
	}

	if old != "github.com/example/mod@v1.2.3" {
		t.Fatalf("expected old value to include expanded version, got %s", old)
	}

	if new != "../local/v1.2.3" {
		t.Fatalf("expected new value to include expanded version, got %s", new)
	}

	module, target, err := gen.Require[0].parts()
	if err != nil {
		t.Fatalf("unexpected error retrieving parsed require parts: %v", err)
	}

	if module != "github.com/example/mod" {
		t.Fatalf("expected module to remain unchanged, got %s", module)
	}

	if target != "github.com/example/mod@v1.2.3" {
		t.Fatalf("expected target to include expanded version, got %s", target)
	}
}

func TestGitconfigGeneratorScriptIncludesReplace(t *testing.T) {
	gen := &SourceGenerator{
		Gomod: &GeneratorGomod{
			Replace: []GomodReplace{
				normalizeGomodReplace("github.com/example/mod@v1.2.3", "../mod"),
			},
			Require: []GomodRequire{
				normalizeGomodRequire("github.com/example/mod", "github.com/example/mod@v1.2.3"),
			},
		},
	}

	st := gen.gitconfigGeneratorScript("gomod.sh")
	def, err := st.Marshal(context.Background())
	if err != nil {
		t.Fatalf("failed to marshal script state: %v", err)
	}

	var script string
	var op pb.Op
	for _, dt := range def.Def {
		if err := op.Unmarshal(dt); err != nil {
			t.Fatalf("failed to unmarshal op: %v", err)
		}

		file := op.GetFile()
		if file == nil {
			continue
		}

		for _, action := range file.Actions {
			if mk := action.GetMkfile(); mk != nil {
				script = string(mk.Data)
				break
			}
		}

		if script != "" {
			break
		}
	}

	if script == "" {
		t.Fatal("failed to locate gomod script mkfile in definition")
	}

	if !strings.Contains(script, "set -eu") {
		t.Fatalf("expected script to enable strict mode, script:\n%s", script)
	}

	if !strings.Contains(script, "if [ ! -f go.mod ]; then") {
		t.Fatalf("expected script to guard against missing go.mod, script:\n%s", script)
	}

	expectedReplace := "go mod edit -replace=\"github.com/example/mod@v1.2.3=../mod\""
	if !strings.Contains(script, expectedReplace) {
		t.Fatalf("expected script to apply replace directive %q, script:\n%s", expectedReplace, script)
	}

	expectedRequire := "go mod edit -require=\"github.com/example/mod@v1.2.3\""
	if !strings.Contains(script, expectedRequire) {
		t.Fatalf("expected script to apply require directive %q, script:\n%s", expectedRequire, script)
	}

	if !strings.Contains(script, "go mod download") {
		t.Fatalf("expected script to invoke go mod download, script:\n%s", script)
	}

	loopCmd := "go list -mod=mod -m -f '{{if and (not .Main) (ne .Version \"\")}}{{.Path}}@{{.Version}}{{end}}' all"
	if !strings.Contains(script, loopCmd) {
		t.Fatalf("expected script to enumerate required modules with %q, script:\n%s", loopCmd, script)
	}

	if !strings.Contains(script, "for mod in $(go list -mod=mod -m") {
		t.Fatalf("expected script to download each module reported by go list, script:\n%s", script)
	}

	if strings.Contains(script, "go env -w GOPRIVATE=") {
		t.Fatalf("expected GOPRIVATE to be skipped when no auth is configured, script:\n%s", script)
	}

	if strings.Contains(script, "go env -w GOINSECURE=") {
		t.Fatalf("expected GOINSECURE to be skipped when no auth is configured, script:\n%s", script)
	}
}

func TestGitconfigGeneratorScriptConfiguresGoEnvForAuth(t *testing.T) {
	gen := &SourceGenerator{
		Gomod: &GeneratorGomod{
			Auth: map[string]GomodGitAuth{
				"git.example.com": {
					Token: "example-token",
				},
			},
		},
	}

	st := gen.gitconfigGeneratorScript("gomod.sh")
	def, err := st.Marshal(context.Background())
	if err != nil {
		t.Fatalf("failed to marshal script state: %v", err)
	}

	var script string
	var op pb.Op
	for _, dt := range def.Def {
		if err := op.Unmarshal(dt); err != nil {
			t.Fatalf("failed to unmarshal op: %v", err)
		}

		file := op.GetFile()
		if file == nil {
			continue
		}

		for _, action := range file.Actions {
			if mk := action.GetMkfile(); mk != nil {
				script = string(mk.Data)
				break
			}
		}

		if script != "" {
			break
		}
	}

	if script == "" {
		t.Fatal("failed to locate gomod script in definition")
	}

	if !strings.Contains(script, "go env -w GOPRIVATE=git.example.com") {
		t.Fatalf("expected script to configure GOPRIVATE, script:\n%s", script)
	}

	if !strings.Contains(script, "go env -w GOINSECURE=git.example.com") {
		t.Fatalf("expected script to configure GOINSECURE, script:\n%s", script)
	}
}

func TestSourceHasGomodDirectives(t *testing.T) {
	tests := []struct {
		name     string
		source   Source
		expected bool
	}{
		{
			name: "no generators",
			source: Source{
				Inline: &SourceInline{Dir: &SourceInlineDir{}},
			},
			expected: false,
		},
		{
			name: "gomod without directives",
			source: Source{
				Inline: &SourceInline{Dir: &SourceInlineDir{}},
				Generate: []*SourceGenerator{
					{Gomod: &GeneratorGomod{}},
				},
			},
			expected: false,
		},
		{
			name: "gomod with replace directive",
			source: Source{
				Inline: &SourceInline{Dir: &SourceInlineDir{}},
				Generate: []*SourceGenerator{
					{
						Gomod: &GeneratorGomod{
							Replace: []GomodReplace{
								"github.com/example/mod@v1.2.3:../mod",
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "gomod with require directive",
			source: Source{
				Inline: &SourceInline{Dir: &SourceInlineDir{}},
				Generate: []*SourceGenerator{
					{
						Gomod: &GeneratorGomod{
							Require: []GomodRequire{
								"github.com/example/mod:github.com/example/mod@v1.2.3",
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "gomod with both directives",
			source: Source{
				Inline: &SourceInline{Dir: &SourceInlineDir{}},
				Generate: []*SourceGenerator{
					{
						Gomod: &GeneratorGomod{
							Replace: []GomodReplace{
								"github.com/example/mod@v1.2.3:../mod",
							},
							Require: []GomodRequire{
								"github.com/example/mod:github.com/example/mod@v1.2.3",
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "multiple generators, one with directives",
			source: Source{
				Inline: &SourceInline{Dir: &SourceInlineDir{}},
				Generate: []*SourceGenerator{
					{Gomod: &GeneratorGomod{}},
					{
						Gomod: &GeneratorGomod{
							Replace: []GomodReplace{
								"github.com/example/mod@v1.2.3:../mod",
							},
						},
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.source.sourceHasGomodDirectives()
			if result != tt.expected {
				t.Errorf("sourceHasGomodDirectives() = %v, expected %v", result, tt.expected)
			}
		})
	}
}
