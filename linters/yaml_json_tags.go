package linters

import (
	"flag"
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// NewYamlJSONTagsAnalyzer creates an analyzer that checks struct tags for json
// and yaml use the same name in types reachable from the root type.
func NewYamlJSONTagsAnalyzer() *analysis.Analyzer {
	linter := &structTagLinter{
		// This tool is designed primarily for use with dalec.Spec
		// Customizable mainly for testing purposes.
		rootType: "github.com/project-dalec/dalec.Spec",
	}

	var flags flag.FlagSet
	flags.StringVar(&linter.rootType, "type",
		linter.rootType,
		"fully qualified type to use as root for validation")

	return &analysis.Analyzer{
		Name:  "yaml_json_names_match",
		Doc:   "check that struct tags for json and yaml use the same name in types reachable from the root type",
		Run:   linter.Run,
		Flags: flags,
	}
}

type structTagLinter struct {
	rootType string // fully qualified type reference, e.g. "github.com/project-dalec/dalec.Spec"
}

func (l *structTagLinter) Run(pass *analysis.Pass) (interface{}, error) {
	// Determine which types to validate based on reachability from root type
	var reachableTypes map[*types.Named]bool

	// Only use type-scoping if type information is available
	if pass.Pkg != nil && pass.TypesInfo != nil {
		pkgPath, typeName := parseTypeRef(l.rootType)
		root := findType(pass, pkgPath, typeName)
		if root == nil {
			// Package doesn't define or import the root type - skip validation
			return nil, nil
		}
		reachableTypes = make(map[*types.Named]bool)
		collectReachableTypes(root, reachableTypes)
	}

	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			typeSpec, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}

			structType, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				return true
			}

			// If we have type scoping, check if this type is reachable from root
			if reachableTypes != nil {
				if !l.isReachable(pass, typeSpec, reachableTypes) {
					return true
				}
			}

			l.checkStructTags(structType, pass)
			return true
		})
	}
	return nil, nil
}

// parseTypeRef splits a fully qualified type reference into package path and type name.
// e.g. "github.com/project-dalec/dalec.Spec" -> ("github.com/project-dalec/dalec", "Spec")
func parseTypeRef(ref string) (pkgPath, typeName string) {
	idx := strings.LastIndex(ref, ".")
	if idx == -1 {
		return "", ref
	}
	return ref[:idx], ref[idx+1:]
}

// findType locates a type by package path and type name.
// Returns nil if not found (package doesn't define or import the type).
func findType(pass *analysis.Pass, pkgPath, typeName string) *types.Named {
	var pkg *types.Package

	if pass.Pkg.Path() == pkgPath {
		pkg = pass.Pkg
	} else {
		for _, imp := range pass.Pkg.Imports() {
			if imp.Path() == pkgPath {
				pkg = imp
				break
			}
		}
	}

	if pkg == nil {
		return nil
	}

	obj := pkg.Scope().Lookup(typeName)
	if obj == nil {
		return nil
	}

	tn, ok := obj.(*types.TypeName)
	if !ok {
		return nil
	}

	named, _ := tn.Type().(*types.Named)
	return named
}

// collectReachableTypes recursively collects all named types
// reachable from the given root type.
func collectReachableTypes(root types.Type, visited map[*types.Named]bool) {
	switch t := root.(type) {
	case *types.Named:
		if visited[t] {
			return
		}
		visited[t] = true
		collectReachableTypes(t.Underlying(), visited)

	case *types.Struct:
		for i := 0; i < t.NumFields(); i++ {
			collectReachableTypes(t.Field(i).Type(), visited)
		}

	case *types.Pointer:
		collectReachableTypes(t.Elem(), visited)

	case *types.Slice:
		collectReachableTypes(t.Elem(), visited)

	case *types.Array:
		collectReachableTypes(t.Elem(), visited)

	case *types.Map:
		collectReachableTypes(t.Key(), visited)
		collectReachableTypes(t.Elem(), visited)
	}
}

// isReachable checks if the given type declaration is in the set of reachable types.
func (l *structTagLinter) isReachable(pass *analysis.Pass, typeSpec *ast.TypeSpec, reachable map[*types.Named]bool) bool {
	obj := pass.TypesInfo.Defs[typeSpec.Name]
	if obj == nil {
		return false
	}

	typeName, ok := obj.(*types.TypeName)
	if !ok {
		return false
	}

	named, ok := typeName.Type().(*types.Named)
	if !ok {
		return false
	}

	return reachable[named]
}

func (l *structTagLinter) checkStructTags(structType *ast.StructType, pass *analysis.Pass) {
	for _, field := range structType.Fields.List {
		if field.Tag != nil {
			tag := field.Tag.Value

			v := getYamlJSONNames(tag)

			var checkTags bool
			if v[0] != "" || v[1] != "" {
				checkTags = true
			}

			if checkTags && v[0] != v[1] {
				pass.Reportf(field.Pos(), "mismatch in struct tags: json=%s, yaml=%s", v[0], v[1])
			}
		}
	}
}

func getYamlJSONNames(tag string) [2]string {
	const (
		yaml = "yaml"
		json = "json"
	)

	tag = strings.Trim(tag, "`")

	var out [2]string
	for _, tag := range strings.Fields(tag) {
		key, tag, _ := strings.Cut(tag, ":")

		value := strings.Trim(tag, `"`)

		switch key {
		case json:
			t, _, _ := strings.Cut(value, ",")
			out[0] = t
		case yaml:
			t, _, _ := strings.Cut(value, ",")
			out[1] = t
		}

		if out[0] != "" && out[1] != "" {
			break
		}
	}

	return out
}
