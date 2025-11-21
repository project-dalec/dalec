package dalec

import (
	"encoding/json"
	"testing"

	"github.com/goccy/go-yaml"
)

func TestGomodRequireUnmarshal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		input           string
		unmarshal       func([]byte, interface{}) error
		expectErr       bool
		expectedModule  string
		expectedVersion string
	}{
		{
			name:            "YAML string format",
			input:           `"github.com/stretchr/testify:github.com/stretchr/testify@v1.8.0"`,
			unmarshal:       yaml.Unmarshal,
			expectErr:       false,
			expectedModule:  "github.com/stretchr/testify",
			expectedVersion: "github.com/stretchr/testify@v1.8.0",
		},
		{
			name: "YAML struct format",
			input: `
module: github.com/cpuguy83/tar2go
version: github.com/cpuguy83/tar2go@v0.3.1
`,
			unmarshal:       yaml.Unmarshal,
			expectErr:       false,
			expectedModule:  "github.com/cpuguy83/tar2go",
			expectedVersion: "github.com/cpuguy83/tar2go@v0.3.1",
		},
		{
			name:            "JSON string format",
			input:           `"github.com/stretchr/testify:github.com/stretchr/testify@v1.8.0"`,
			unmarshal:       json.Unmarshal,
			expectErr:       false,
			expectedModule:  "github.com/stretchr/testify",
			expectedVersion: "github.com/stretchr/testify@v1.8.0",
		},
		{
			name:            "JSON struct format",
			input:           `{"module":"github.com/cpuguy83/tar2go","version":"github.com/cpuguy83/tar2go@v0.3.1"}`,
			unmarshal:       json.Unmarshal,
			expectErr:       false,
			expectedModule:  "github.com/cpuguy83/tar2go",
			expectedVersion: "github.com/cpuguy83/tar2go@v0.3.1",
		},
		{
			name:      "invalid string format - no colon",
			input:     `"github.com/stretchr/testify"`,
			unmarshal: yaml.Unmarshal,
			expectErr: true,
		},
		{
			name:      "invalid string format - no @version",
			input:     `"github.com/stretchr/testify:v1.8.0"`,
			unmarshal: yaml.Unmarshal,
			expectErr: true,
		},
		{
			name:      "invalid struct format - missing version",
			input:     `{"module":"github.com/cpuguy83/tar2go"}`,
			unmarshal: json.Unmarshal,
			expectErr: true,
		},
		{
			name:      "invalid struct format - version without @",
			input:     `{"module":"github.com/cpuguy83/tar2go","version":"v0.3.1"}`,
			unmarshal: json.Unmarshal,
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req GomodRequire
			err := tt.unmarshal([]byte(tt.input), &req)

			if tt.expectErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}

			if req.Module != tt.expectedModule {
				t.Errorf("expected Module=%q, got %q", tt.expectedModule, req.Module)
			}
			if req.Version != tt.expectedVersion {
				t.Errorf("expected Version=%q, got %q", tt.expectedVersion, req.Version)
			}
		})
	}
}

func TestGomodReplaceUnmarshal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		unmarshal   func([]byte, interface{}) error
		expectErr   bool
		expectedOld string
		expectedNew string
	}{
		{
			name:        "YAML string format",
			input:       `"github.com/stretchr/testify:github.com/stretchr/testify@v1.8.0"`,
			unmarshal:   yaml.Unmarshal,
			expectErr:   false,
			expectedOld: "github.com/stretchr/testify",
			expectedNew: "github.com/stretchr/testify@v1.8.0",
		},
		{
			name: "YAML struct format",
			input: `
old: github.com/cpuguy83/tar2go
new: github.com/cpuguy83/tar2go@v0.3.1
`,
			unmarshal:   yaml.Unmarshal,
			expectErr:   false,
			expectedOld: "github.com/cpuguy83/tar2go",
			expectedNew: "github.com/cpuguy83/tar2go@v0.3.1",
		},
		{
			name:        "JSON string format",
			input:       `"github.com/stretchr/testify:github.com/stretchr/testify@v1.8.0"`,
			unmarshal:   json.Unmarshal,
			expectErr:   false,
			expectedOld: "github.com/stretchr/testify",
			expectedNew: "github.com/stretchr/testify@v1.8.0",
		},
		{
			name:        "JSON struct format",
			input:       `{"old":"github.com/cpuguy83/tar2go","new":"github.com/cpuguy83/tar2go@v0.3.1"}`,
			unmarshal:   json.Unmarshal,
			expectErr:   false,
			expectedOld: "github.com/cpuguy83/tar2go",
			expectedNew: "github.com/cpuguy83/tar2go@v0.3.1",
		},
		{
			name:      "invalid string format - no colon",
			input:     `"github.com/stretchr/testify"`,
			unmarshal: yaml.Unmarshal,
			expectErr: true,
		},
		{
			name:      "invalid struct format - missing new",
			input:     `{"old":"github.com/cpuguy83/tar2go"}`,
			unmarshal: json.Unmarshal,
			expectErr: true,
		},
		{
			name:      "invalid struct format - empty old",
			input:     `{"old":"","new":"github.com/cpuguy83/tar2go@v0.3.1"}`,
			unmarshal: json.Unmarshal,
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var repl GomodReplace
			err := tt.unmarshal([]byte(tt.input), &repl)

			if tt.expectErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}

			if repl.Original != tt.expectedOld {
				t.Errorf("expected Old=%q, got %q", tt.expectedOld, repl.Original)
			}
			if repl.Update != tt.expectedNew {
				t.Errorf("expected New=%q, got %q", tt.expectedNew, repl.Update)
			}
		})
	}
}

func TestGomodRequireGoModEditArg(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		req         GomodRequire
		expectErr   bool
		expectedArg string
	}{
		{
			name: "valid require",
			req: GomodRequire{
				Module:  "github.com/stretchr/testify",
				Version: "github.com/stretchr/testify@v1.8.0",
			},
			expectErr:   false,
			expectedArg: "github.com/stretchr/testify@v1.8.0",
		},
		{
			name: "missing @version",
			req: GomodRequire{
				Module:  "github.com/stretchr/testify",
				Version: "v1.8.0",
			},
			expectErr: true,
		},
		{
			name: "empty module",
			req: GomodRequire{
				Module:  "",
				Version: "github.com/stretchr/testify@v1.8.0",
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			arg, err := tt.req.goModEditArg()

			if tt.expectErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if arg != tt.expectedArg {
				t.Errorf("expected %q, got %q", tt.expectedArg, arg)
			}
		})
	}
}

func TestGomodReplaceGoModEditArg(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		repl        GomodReplace
		expectErr   bool
		expectedArg string
	}{
		{
			name: "valid replace",
			repl: GomodReplace{
				Original: "github.com/stretchr/testify",
				Update:      "github.com/stretchr/testify@v1.8.0",
			},
			expectErr:   false,
			expectedArg: "github.com/stretchr/testify=github.com/stretchr/testify@v1.8.0",
		},
		{
			name: "empty old",
			repl: GomodReplace{
				Original: "",
				Update:      "github.com/stretchr/testify@v1.8.0",
			},
			expectErr: true,
		},
		{
			name: "empty new",
			repl: GomodReplace{
				Original: "github.com/stretchr/testify",
				Update:      "",
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			arg, err := tt.repl.goModEditArg()

			if tt.expectErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if arg != tt.expectedArg {
				t.Errorf("expected %q, got %q", tt.expectedArg, arg)
			}
		})
	}
}
