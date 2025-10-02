package dalec

import (
	"strings"
	"testing"
)

func TestGomodEditScriptIncludesGoModTidy(t *testing.T) {
	spec := &Spec{
		Sources: map[string]Source{
			"src": {
				Inline: &SourceInline{Dir: &SourceInlineDir{}},
				Generate: []*SourceGenerator{
					{
						Gomod: &GeneratorGomod{
							Require: []GomodRequire{
								normalizeGomodRequire("github.com/example/mod", "github.com/example/mod@v1.0.0"),
							},
						},
					},
				},
			},
		},
	}

	script := GomodEditScript(spec)

	if script == "" {
		t.Fatalf("expected gomod edit script to be generated")
	}

	if !strings.Contains(script, "go mod tidy") {
		t.Fatalf("expected script to run go mod tidy, got:\n%s", script)
	}

	if !strings.Contains(script, "go mod download") {
		t.Fatalf("expected script to run go mod download, got:\n%s", script)
	}
}

func TestGomodEditScriptEmptyWhenNoGomod(t *testing.T) {
	spec := &Spec{}

	if script := GomodEditScript(spec); script != "" {
		t.Fatalf("expected empty script when spec has no gomod generators, got: %s", script)
	}
}
