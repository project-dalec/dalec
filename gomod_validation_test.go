package dalec

import (
	"strings"
	"testing"
)

// TestGomodReplaceValidation verifies that GomodReplace directives are properly validated
// for format correctness, ensuring old:new syntax and non-empty components.
func TestGomodReplaceValidation(t *testing.T) {
	tests := []struct {
		name        string
		replace     string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "valid replace with version",
			replace:     "github.com/example/mod@v1.0.0:../local",
			expectError: false,
		},
		{
			name:        "valid replace without version",
			replace:     "github.com/example/mod:../local",
			expectError: false,
		},
		{
			name:        "missing separator",
			replace:     "github.com/example/mod@v1.0.0",
			expectError: true,
			errorMsg:    "expected format old:new",
		},
		{
			name:        "empty old module",
			replace:     ":../local",
			expectError: true,
			errorMsg:    "entries must be non-empty",
		},
		{
			name:        "empty new module",
			replace:     "github.com/example/mod:",
			expectError: true,
			errorMsg:    "entries must be non-empty",
		},
		{
			name:        "empty string",
			replace:     "",
			expectError: true,
			errorMsg:    "expected format old:new",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			replace := GomodReplace(tt.replace)
			_, err := replace.goModEditArg()

			if tt.expectError {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.errorMsg)
				}
				if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Fatalf("expected error containing %q, got %q", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

// TestGomodRequireValidation verifies that GomodRequire directives are properly validated
// for format correctness, ensuring module:target@version syntax with required @version.
func TestGomodRequireValidation(t *testing.T) {
	tests := []struct {
		name        string
		require     string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "valid require with version",
			require:     "github.com/example/mod:github.com/example/mod@v1.0.0",
			expectError: false,
		},
		{
			name:        "missing separator",
			require:     "github.com/example/mod@v1.0.0",
			expectError: true,
			errorMsg:    "expected format module:target@version",
		},
		{
			name:        "empty module name",
			require:     ":github.com/example/mod@v1.0.0",
			expectError: true,
			errorMsg:    "entries must be non-empty",
		},
		{
			name:        "empty target",
			require:     "github.com/example/mod:",
			expectError: true,
			errorMsg:    "entries must be non-empty",
		},
		{
			name:        "empty string",
			require:     "",
			expectError: true,
			errorMsg:    "expected format module:target@version",
		},
		{
			name:        "missing version in target",
			require:     "github.com/example/mod:github.com/example/mod",
			expectError: true,
			errorMsg:    "target must include @version",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require := GomodRequire(tt.require)
			_, err := require.goModEditArg()

			if tt.expectError {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.errorMsg)
				}
				if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Fatalf("expected error containing %q, got %q", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

// TestValidateGomodDirectivesGeneratorLevel verifies that gomod directive validation
// catches errors at the generator level before they cause build failures.
func TestValidateGomodDirectivesGeneratorLevel(t *testing.T) {
	tests := []struct {
		name        string
		gen         *GeneratorGomod
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid replace and require",
			gen: &GeneratorGomod{
				Replace: []GomodReplace{
					"github.com/example/mod@v1.0.0:../local",
				},
				Require: []GomodRequire{
					"github.com/other/mod:github.com/other/mod@v2.0.0",
				},
			},
			expectError: false,
		},
		{
			name: "invalid replace",
			gen: &GeneratorGomod{
				Replace: []GomodReplace{
					"invalid-replace-missing-separator",
				},
			},
			expectError: true,
			errorMsg:    "invalid gomod replace[0]",
		},
		{
			name: "invalid require",
			gen: &GeneratorGomod{
				Require: []GomodRequire{
					"invalid-require-missing-separator",
				},
			},
			expectError: true,
			errorMsg:    "invalid gomod require[0]",
		},
		{
			name: "multiple invalid directives",
			gen: &GeneratorGomod{
				Replace: []GomodReplace{
					"github.com/valid/mod@v1.0.0:../local",
					"invalid-replace",
				},
				Require: []GomodRequire{
					"invalid-require",
				},
			},
			expectError: true,
			errorMsg:    "invalid gomod replace[1]",
		},
		{
			name:        "empty generator",
			gen:         &GeneratorGomod{},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.gen.validateGomodDirectives()

			if tt.expectError {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.errorMsg)
				}
				if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Fatalf("expected error containing %q, got %q", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

// TestValidateGomodDirectivesSpecLevel verifies that gomod directive validation
// works correctly at the spec level, identifying which source and generator has errors.
func TestValidateGomodDirectivesSpecLevel(t *testing.T) {
	tests := []struct {
		name        string
		spec        *Spec
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid spec with gomod",
			spec: &Spec{
				Sources: map[string]Source{
					"src": {
						Generate: []*SourceGenerator{
							{
								Gomod: &GeneratorGomod{
									Replace: []GomodReplace{
										"github.com/example/mod@v1.0.0:../local",
									},
								},
							},
						},
					},
				},
			},
			expectError: false,
		},
		{
			name: "invalid gomod in source",
			spec: &Spec{
				Sources: map[string]Source{
					"mysource": {
						Generate: []*SourceGenerator{
							{
								Gomod: &GeneratorGomod{
									Replace: []GomodReplace{
										"invalid-replace",
									},
								},
							},
						},
					},
				},
			},
			expectError: true,
			errorMsg:    "source \"mysource\" generator[0]",
		},
		{
			name: "multiple sources with invalid in second",
			spec: &Spec{
				Sources: map[string]Source{
					"valid": {
						Generate: []*SourceGenerator{
							{
								Gomod: &GeneratorGomod{
									Replace: []GomodReplace{
										"github.com/example/mod@v1.0.0:../local",
									},
								},
							},
						},
					},
					"invalid": {
						Generate: []*SourceGenerator{
							{
								Gomod: &GeneratorGomod{
									Require: []GomodRequire{
										"bad-require",
									},
								},
							},
						},
					},
				},
			},
			expectError: true,
			errorMsg:    "source \"invalid\" generator[0]",
		},
		{
			name: "spec without gomod",
			spec: &Spec{
				Sources: map[string]Source{
					"src": {
						Inline: &SourceInline{
							Dir: &SourceInlineDir{},
						},
					},
				},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.spec.validateGomodDirectives()

			if tt.expectError {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.errorMsg)
				}
				if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Fatalf("expected error containing %q, got %q", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

// TestLoadSpecValidatesGomod verifies that LoadSpec performs early validation of
// gomod directives during spec loading, catching errors before build execution.
func TestLoadSpecValidatesGomod(t *testing.T) {
	// Valid YAML with valid gomod directives
	validYAML := []byte(`
name: test
sources:
  src:
    inline:
      dir: {}
    generate:
      - gomod:
          replace:
            - "github.com/example/mod@v1.0.0:../local"
`)

	spec, err := LoadSpec(validYAML)
	if err != nil {
		t.Fatalf("unexpected error loading valid spec: %v", err)
	}
	if spec == nil {
		t.Fatal("expected spec to be loaded")
	}

	// Invalid YAML with bad gomod directives
	invalidYAML := []byte(`
name: test
sources:
  src:
    inline:
      dir: {}
    generate:
      - gomod:
          replace:
            - "invalid-replace-missing-separator"
`)

	_, err = LoadSpec(invalidYAML)
	if err == nil {
		t.Fatal("expected error loading spec with invalid gomod directives")
	}
	// The error happens during unmarshalling, which is before our validation
	// This is expected - YAML unmarshalling validates the format
	if !strings.Contains(err.Error(), "invalid gomod replace") {
		t.Fatalf("expected invalid gomod replace error, got: %v", err)
	}
}

// TestGomodEditScriptCombinedReplaceAndRequire verifies that the script generator
// correctly handles specs with both replace and require directives together.
func TestGomodEditScriptCombinedReplaceAndRequire(t *testing.T) {
	spec := &Spec{
		Sources: map[string]Source{
			"src": {
				Inline: &SourceInline{Dir: &SourceInlineDir{}},
				Generate: []*SourceGenerator{
					{
						Gomod: &GeneratorGomod{
							Replace: []GomodReplace{
								"github.com/example/mod@v1.0.0:../local",
								"github.com/other/mod:../other",
							},
							Require: []GomodRequire{
								"github.com/required/mod:github.com/required/mod@v2.0.0",
								"github.com/another/mod:github.com/another/mod@v3.0.0",
							},
						},
					},
				},
			},
		},
	}

	script, err := GomodEditScript(spec)
	if err != nil {
		t.Fatalf("unexpected error generating script: %v", err)
	}

	if script == "" {
		t.Fatal("expected non-empty script")
	}

	// Check that all replace directives are present
	expectedReplaces := []string{
		`go mod edit -replace="github.com/example/mod@v1.0.0=../local"`,
		`go mod edit -replace="github.com/other/mod=../other"`,
	}
	for _, expected := range expectedReplaces {
		if !strings.Contains(script, expected) {
			t.Errorf("expected script to contain %q, got:\n%s", expected, script)
		}
	}

	// Check that all require directives are present
	expectedRequires := []string{
		`go mod edit -require="github.com/required/mod@v2.0.0"`,
		`go mod edit -require="github.com/another/mod@v3.0.0"`,
	}
	for _, expected := range expectedRequires {
		if !strings.Contains(script, expected) {
			t.Errorf("expected script to contain %q, got:\n%s", expected, script)
		}
	}

	// Verify go.mod checks are present (checks specific paths)
	if !strings.Contains(script, "if [ -f") || !strings.Contains(script, "go.mod") {
		t.Error("expected script to check for go.mod existence")
	}

	// Verify go mod tidy and download are present
	if !strings.Contains(script, "go mod tidy") {
		t.Error("expected script to run go mod tidy")
	}
	if !strings.Contains(script, "go mod download") {
		t.Error("expected script to run go mod download")
	}
}

// TestGomodEditScriptMultipleSources verifies that the script generator correctly
// handles multiple sources with different gomod directives in the same spec.
func TestGomodEditScriptMultipleSources(t *testing.T) {
	spec := &Spec{
		Sources: map[string]Source{
			"src1": {
				Inline: &SourceInline{Dir: &SourceInlineDir{}},
				Generate: []*SourceGenerator{
					{
						Gomod: &GeneratorGomod{
							Replace: []GomodReplace{
								"github.com/example/mod@v1.0.0:../local",
							},
						},
					},
				},
			},
			"src2": {
				Inline: &SourceInline{Dir: &SourceInlineDir{}},
				Generate: []*SourceGenerator{
					{
						Gomod: &GeneratorGomod{
							Require: []GomodRequire{
								"github.com/required/mod:github.com/required/mod@v2.0.0",
							},
						},
					},
				},
			},
		},
	}

	script, err := GomodEditScript(spec)
	if err != nil {
		t.Fatalf("unexpected error generating script: %v", err)
	}

	if script == "" {
		t.Fatal("expected non-empty script")
	}

	// Both directives from different sources should be present
	if !strings.Contains(script, `go mod edit -replace="github.com/example/mod@v1.0.0=../local"`) {
		t.Error("expected script to contain replace from src1")
	}
	if !strings.Contains(script, `go mod edit -require="github.com/required/mod@v2.0.0"`) {
		t.Error("expected script to contain require from src2")
	}
}
