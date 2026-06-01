package dalec

import _ "embed"

// schemaJSON contains the embedded JSON schema for Dalec specs.
// This is generated at build time by running `go generate`.
//
//go:embed docs/spec.schema.json
var schemaJSON []byte

// GetJSONSchema returns the embedded JSON schema for Dalec specs.
func GetJSONSchema() []byte {
	return schemaJSON
}
