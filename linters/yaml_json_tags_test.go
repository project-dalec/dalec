package linters

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/assert"
	"golang.org/x/tools/go/analysis"
)

func TestCheckStructTags(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		expected []string
	}{
		{
			name: "matching tags",
			src: `
				package test
				type Test struct {
					Field1 string ` + "`json:\"field1\" yaml:\"field1\"`" + `
				}
			`,
			expected: nil,
		},
		{
			name: "mismatched tags",
			src: `
				package test
				type Test struct {
					Field1 string ` + "`json:\"field1\" yaml:\"field2\"`" + `
				}
			`,
			expected: []string{"mismatch in struct tags: json=field1, yaml=field2"},
		},
		{
			name: "missing json tag",
			src: `
				package test
				type Test struct {
					Field1 string ` + "`yaml:\"field1\"`" + `
				}
			`,
			expected: []string{"mismatch in struct tags: json=, yaml=field1"},
		},
		{
			name: "missing yaml tag",
			src: `
				package test
				type Test struct {
					Field1 string ` + "`json:\"field1\"`" + `
				}
			`,
			expected: []string{"mismatch in struct tags: json=field1, yaml="},
		},
		{
			name: "no tags",
			src: `
				package test
				type Test struct {
					Field1 string
				}
			`,
			expected: nil,
		},
		{
			name: "extra spaces",
			src: `
				package test
				type Test struct {
					Field1 string ` + "`json:\"field1\"   yaml:\"field1\"`" + `
				}
			`,
			expected: nil,
		},
		{
			name: "reversed order",
			src: `
				package test
				type Test struct {
					Field1 string ` + "`yaml:\"field1\" json:\"field1\"`" + `
				}
			`,
			expected: nil,
		},
		{
			name: "extra spaces and mismatched tags",
			src: `
				package test
				type Test struct {
					Field1 string ` + "`json:\"field1\"   yaml:\"field2\"`" + `
				}
			`,
			expected: []string{"mismatch in struct tags: json=field1, yaml=field2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fset := token.NewFileSet()
			node, err := parser.ParseFile(fset, "test.go", tt.src, parser.ParseComments)
			if err != nil {
				t.Fatalf("failed to parse source: %v", err)
			}

			var reports []string
			pass := &analysis.Pass{
				Fset:  fset,
				Files: []*ast.File{node},
				Report: func(d analysis.Diagnostic) {
					reports = append(reports, d.Message)
				},
			}

			linter := structTagLinter{}
			_, err = linter.Run(pass)
			assert.NoError(t, err)

			assert.Len(t, reports, len(tt.expected))
			assert.Equal(t, tt.expected, reports)
		})
	}
}

func TestGetYamlJSONNames(t *testing.T) {
	tests := []struct {
		tag      string
		expected [2]string
	}{
		{
			tag:      "`json:\"field1\" yaml:\"field1\"`",
			expected: [2]string{"field1", "field1"},
		},
		{
			tag:      "`json:\"field1\" yaml:\"field2\"`",
			expected: [2]string{"field1", "field2"},
		},
		{
			tag:      "`json:\"field1\"`",
			expected: [2]string{"field1", ""},
		},
		{
			tag:      "`yaml:\"field1\"`",
			expected: [2]string{"", "field1"},
		},
		{
			tag:      "`json:\"field1,omitempty\" yaml:\"field1\"`",
			expected: [2]string{"field1", "field1"},
		},
		{
			tag:      "`json:\"field1\" yaml:\"field1,omitempty\"`",
			expected: [2]string{"field1", "field1"},
		},
		{
			tag:      "`json:\"field1\"   yaml:\"field1\"`",
			expected: [2]string{"field1", "field1"},
		},
		{
			tag:      "`yaml:\"field1\" json:\"field1\"`",
			expected: [2]string{"field1", "field1"},
		},
		{
			tag:      "`json:\"field1\"   yaml:\"field2\"`",
			expected: [2]string{"field1", "field2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.tag, func(t *testing.T) {
			result := getYamlJSONNames(tt.tag)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}
