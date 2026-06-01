package dalec

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/goccy/go-yaml"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestDate(t *testing.T) {
	expect := "2023-10-01"
	expectTime, err := time.Parse(time.DateOnly, expect)
	assert.NilError(t, err)

	d := Date{Time: expectTime}
	assert.Check(t, cmp.Equal(d.Format(time.DateOnly), expect))

	dtJSON, err := json.Marshal(d)
	assert.NilError(t, err)

	dtYAML, err := yaml.Marshal(d)
	assert.NilError(t, err)

	var d2 Date
	err = json.Unmarshal(dtJSON, &d2)
	assert.NilError(t, err)
	assert.Check(t, cmp.Equal(d2.Format(time.DateOnly), expect))

	d3 := Date{}
	err = yaml.Unmarshal(dtYAML, &d3)
	assert.NilError(t, err)
	assert.Check(t, cmp.Equal(d3.Format(time.DateOnly), expect))
}

func TestSourceFilterConfig(t *testing.T) {
	t.Parallel()

	var cfg SourceFilterConfig
	err := yaml.Unmarshal([]byte(`
global_excludes:
  - github.com/klauspost/compress@*/zip/corpus/14.zip
  - cache/download/github.com/klauspost/compress/@v/*.zip
`), &cfg)
	assert.NilError(t, err)
	assert.Check(t, cmp.DeepEqual(cfg.GlobalExcludes, []string{
		"github.com/klauspost/compress@*/zip/corpus/14.zip",
		"cache/download/github.com/klauspost/compress/@v/*.zip",
	}))
	assert.Check(t, !cfg.IsEmpty())

	var empty SourceFilterConfig
	assert.Check(t, empty.IsEmpty())
}

func TestSourceGeneratorValidateGomodEdits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		gen               *SourceGenerator
		expectErr         bool
		expectedErrSubstr string
	}{
		{
			name: "valid gomod edits",
			gen: &SourceGenerator{
				Gomod: &GeneratorGomod{
					Edits: &GomodEdits{
						Replace: []GomodReplace{
							{Original: "github.com/old/module", Update: "github.com/new/module@v1.0.0"},
						},
					},
				},
			},
			expectErr: false,
		},
		{
			name: "invalid replace - empty old",
			gen: &SourceGenerator{
				Gomod: &GeneratorGomod{
					Edits: &GomodEdits{
						Replace: []GomodReplace{
							{Original: "", Update: "github.com/new/module@v1.0.0"},
						},
					},
				},
			},
			expectErr:         true,
			expectedErrSubstr: "must be non-empty",
		},
		{
			name: "invalid replace - empty new",
			gen: &SourceGenerator{
				Gomod: &GeneratorGomod{
					Edits: &GomodEdits{
						Replace: []GomodReplace{
							{Original: "github.com/old/module", Update: ""},
						},
					},
				},
			},
			expectErr:         true,
			expectedErrSubstr: "must be non-empty",
		},
		{
			name: "multiple errors",
			gen: &SourceGenerator{
				Gomod: &GeneratorGomod{
					Edits: &GomodEdits{
						Replace: []GomodReplace{
							{Original: "", Update: ""}, // Both invalid
						},
					},
				},
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.gen.Validate()

			if tt.expectErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.expectedErrSubstr != "" {
					assert.Check(t, cmp.Contains(err.Error(), tt.expectedErrSubstr))
				}
				return
			}

			assert.NilError(t, err)
		})
	}
}
