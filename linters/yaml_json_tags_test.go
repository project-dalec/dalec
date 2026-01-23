package linters

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"golang.org/x/tools/go/analysis"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
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

			// Use a linter without type info - falls back to checking all structs
			linter := &structTagLinter{}
			_, err = linter.Run(pass)
			assert.NilError(t, err)

			assert.Assert(t, cmp.Len(reports, len(tt.expected)))
			assert.Assert(t, cmp.DeepEqual(reports, tt.expected))
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

func TestTypeScoping(t *testing.T) {
	const testPkgPath = "example.com/testpkg"

	// Source with Spec type and reachable/unreachable types
	src := `
package testpkg

type Spec struct {
	Name   string  ` + "`json:\"name\" yaml:\"name\"`" + `
	Nested *Nested ` + "`json:\"nested\" yaml:\"nested\"`" + `
}

type Nested struct {
	// This should be validated (reachable from Spec)
	Value string ` + "`json:\"value\" yaml:\"wrong\"`" + `
}

type Unrelated struct {
	// This should NOT be validated (not reachable from Spec)
	Data string ` + "`json:\"data\" yaml:\"different\"`" + `
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "spec.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	// Type-check to get full type information
	conf := types.Config{Importer: importer.Default()}
	info := &types.Info{
		Defs: make(map[*ast.Ident]types.Object),
	}
	pkg, err := conf.Check(testPkgPath, fset, []*ast.File{file}, info)
	if err != nil {
		t.Fatalf("type check failed: %v", err)
	}

	var reports []string
	pass := &analysis.Pass{
		Fset:      fset,
		Files:     []*ast.File{file},
		Pkg:       pkg,
		TypesInfo: info,
		Report: func(d analysis.Diagnostic) {
			reports = append(reports, d.Message)
		},
	}

	// Configure linter to use our test package's Spec as root type
	linter := &structTagLinter{
		rootType: testPkgPath + ".Spec",
	}
	_, err = linter.Run(pass)
	if err != nil {
		t.Fatalf("linter failed: %v", err)
	}

	// Should only report the mismatch in Nested, not Unrelated
	expected := []string{"mismatch in struct tags: json=value, yaml=wrong"}
	assert.Assert(t, cmp.DeepEqual(reports, expected))
}

func TestTypeScopingSkipsUnrelatedPackages(t *testing.T) {
	// Source for a package that doesn't match the root type's package
	src := `
package unrelated

type Config struct {
	// This has mismatched tags but should NOT be validated
	// because this package doesn't contain or import the root type
	Data string ` + "`json:\"data\" yaml:\"different\"`" + `
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "config.go", src, parser.ParseComments)
	assert.NilError(t, err)

	// Type-check to get full type information
	conf := types.Config{Importer: importer.Default()}
	info := &types.Info{
		Defs: make(map[*ast.Ident]types.Object),
	}
	pkg, err := conf.Check("example.com/unrelated", fset, []*ast.File{file}, info)
	assert.NilError(t, err)

	var reports []string
	pass := &analysis.Pass{
		Fset:      fset,
		Files:     []*ast.File{file},
		Pkg:       pkg,
		TypesInfo: info,
		Report: func(d analysis.Diagnostic) {
			reports = append(reports, d.Message)
		},
	}

	// Use default root type - this package doesn't contain or import it
	linter := &structTagLinter{
		rootType: "github.com/project-dalec/dalec.Spec",
	}
	_, err = linter.Run(pass)
	assert.NilError(t, err)

	// Should report nothing - package doesn't contain or import the root type
	assert.Assert(t, cmp.Len(reports, 0), "expected no reports for unrelated package, got: %v", reports)
}
