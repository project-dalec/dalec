package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/atombender/go-jsonschema/pkg/schemas"
	"github.com/invopop/jsonschema"
	"github.com/project-dalec/dalec"
)

func main() {
	dt, err := generateJSONSchema()
	if err != nil {
		panic(err)
	}

	if len(os.Args) > 1 {
		// Write to file
		if err := os.WriteFile(os.Args[1], dt, 0644); err != nil {
			panic(err)
		}
		return
	}

	fmt.Println(string(dt))
}

// generateJSONSchema generates and returns the JSON schema for Dalec specs.
// This schema can be used by editors and tools for validation and autocomplete.
func generateJSONSchema() ([]byte, error) {
	var r jsonschema.Reflector
	if err := r.AddGoComments("github.com/project-dalec/dalec", "./"); err != nil {
		return nil, err
	}

	schema := r.Reflect(&dalec.Spec{})

	if schema.PatternProperties == nil {
		schema.PatternProperties = make(map[string]*jsonschema.Schema)
	}
	schema.PatternProperties["^x-"] = &jsonschema.Schema{}

	dt, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}

	// The above library used is good for generating the schema from the go types,
	// but it doesn't give us everything we need to make manipulations to the schema
	// since the data is not represented in go correctly.
	// So we convert the schema to JSON and then back to another go type that is more
	// suitable for manipulation.
	// Specifically, the problem with the above library is that the `Type` parameter
	// is a string, but it should be a []string.
	// Both are apparently(?) valid jsonschema, but the latter is what we need to
	// fixup the schema to allow null values and other things.

	schema2, err := schemas.FromJSONReader(bytes.NewReader(dt))
	if err != nil {
		return nil, err
	}

	const (
		specKey  = "Spec"
		argsKey  = "args"
		buildKey = "build"
	)

	spec := schema2.Definitions[specKey]
	// Allow args values to be integers (in addition to strings)
	spec.Properties[argsKey].AdditionalProperties.Type = append(
		spec.Properties[argsKey].AdditionalProperties.Type, "integer")

	build := spec.Properties[buildKey]
	buildType := strings.TrimPrefix(build.Ref, "#/$defs/")
	build = schema2.Definitions[buildType]
	// Allow env values to be integers (in addition to strings)
	build.Properties["env"].Type = append(build.Properties["env"].Type, "integer")

	for _, v := range schema2.Definitions {
		setObjectAllowNull(v)
	}

	dt, err = json.MarshalIndent(schema2, "", "\t")
	if err != nil {
		return nil, err
	}

	return dt, nil
}

// setObjectAllowNull recursively walks the schema and adds "null" as an allowed
// type for any non-required field that is an object or string.
func setObjectAllowNull(t *schemas.Type) {
	if t == nil {
		return
	}

	if t.AdditionalProperties != nil {
		setObjectAllowNull(t.AdditionalProperties)
	}

	for k, v := range t.Properties {
		// Skip required fields - they can't be null
		if slices.Contains(t.Required, k) {
			continue
		}
		setObjectAllowNull(v)
		t.Properties[k] = v
	}

	// Check if this type should allow null
	ok := slices.ContainsFunc(t.Type, func(v string) bool {
		if v == "null" {
			// Already allows null, nothing to do.
			return false
		}
		return v == "object" || v == "string"
	})

	if !ok {
		return
	}

	// Add "null" as an allowed type
	t.Type = append(t.Type, "null")
}
