package dalec

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"gotest.tools/v3/assert"
)

func TestUnmarshalNestedAliases(t *testing.T) {
	dt := []byte(strings.TrimLeft(depsSpecHeader, "\n") + `x-text: &text hello
x-uid: &uid 123
x-gid: &gid 456
x-versions: &versions [">= 1", "< 3"]
x-constraint: &constraint
  version: *versions
x-file: &file
  contents: *text
  uid: *uid
  gid: *gid
x-source: &source
  inline:
    file: *file
x-step: &step
  command: echo hello
  env: &env {MESSAGE: *text}
x-steps: &steps
  - *step
  - <<: *step
x-replacement: &replacement
  old: example.com/old
  new: example.com/new@v1.0.0
sources:
  direct: *source
  directory:
    inline:
      dir:
        uid: *uid
        gid: *gid
        files:
          hello: *file
    generate:
      - gomod:
          paths: ["."]
          edits:
            replace:
              - *replacement
              - "example.com/a => example.com/b@v1.0.0"
  nested:
    build:
      source: *source
build:
  env: *env
  steps: *steps
dependencies:
  build:
    pkg: *constraint
  extra_repos:
    - config:
        custom: *source
      envs: [build]
tests:
  - name: output
    steps:
      - <<: *step
        stdout:
          equals: *text
          contains: [*text]
        stderr:
          empty: true
    files:
      /hello:
        permissions: 0644
        no_follow: true
        equals: *text
        contains: [*text]
`)
	spec, err := LoadSpecWithSourceMap("aliases.yml", dt)
	assert.NilError(t, err)

	file := spec.Sources["direct"].Inline.File
	assert.Equal(t, file.Contents, "hello")
	assert.Equal(t, file.UID, 123)
	assert.Equal(t, file.GID, 456)
	dir := spec.Sources["directory"].Inline.Dir
	assert.Equal(t, dir.UID, 123)
	assert.Equal(t, dir.GID, 456)
	assert.Equal(t, dir.Files["hello"].Contents, "hello")
	assert.Equal(t, spec.Sources["nested"].Build.Source.Inline.File.UID, 123)
	assert.Equal(t, spec.Dependencies.ExtraRepos[0].Config["custom"].Inline.File.GID, 456)
	assert.DeepEqual(t, spec.Dependencies.Build["pkg"].Version, []string{">= 1", "< 3"})
	assert.Equal(t, spec.Build.Steps[0].Command, "echo hello")
	assert.Equal(t, spec.Build.Steps[1].Command, "echo hello")
	assert.Equal(t, spec.Build.Steps[1].Env["MESSAGE"], "hello")
	replacements := spec.Sources["directory"].Generate[0].Gomod.GetReplace()
	assert.Equal(t, len(replacements), 2)
	assert.Equal(t, replacements[0].Original, "example.com/old")
	assert.Equal(t, replacements[0].Update, "example.com/new@v1.0.0")
	assert.Equal(t, replacements[1].Update, "example.com/b@v1.0.0")
	step := spec.Tests[0].Steps[0]
	assert.Equal(t, step.Command, "echo hello")
	assert.Equal(t, step.Stdout.Equals, "hello")
	assert.DeepEqual(t, step.Stdout.Contains, []string{"hello"})
	assert.Equal(t, step.Stderr.Empty, true)
	check := spec.Tests[0].Files["/hello"]
	assert.Equal(t, check.Equals, "hello")
	assert.DeepEqual(t, check.Contains, []string{"hello"})
	assert.Equal(t, check.Permissions.String(), "-rw-r--r--")
	assert.Equal(t, check.NoFollow, true)

	// Scalar, mapping, and sequence aliases must retain original document
	// positions, including fields decoded in FileCheckOutput's second pass.
	for _, tc := range []struct {
		name string
		sm   *sourceMap
		line string
	}{
		{"file", file._sourceMap, "contents: *text"},
		{"uid", file._uidSourceMap, "x-uid: &uid"},
		{"gid", dir._gidSourceMap, "x-gid: &gid"},
		{"constraint", spec.Dependencies.Build["pkg"]._sourceMap, "version: *versions"},
		{"command", step._commandSourceMap, "command: echo hello"},
		{"stdout", step.Stdout.equalsSourceMap, "x-text: &text"},
		{"file output", check.equalsSourceMap, "x-text: &text"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Assert(t, tc.sm != nil)
			assert.Equal(t, tc.sm.filename, "aliases.yml")
			assert.Equal(t, tc.sm.language, "yaml")
			assert.DeepEqual(t, tc.sm.data, dt)
			assert.Equal(t, int(tc.sm.pos.Start.Line), lineOf(string(dt), tc.line))
			assert.Assert(t, tc.sm.pos.Start.Character > 0)
			assert.Assert(t, tc.sm.pos.End.Line >= tc.sm.pos.Start.Line)
			assert.Assert(t, tc.sm.pos.End.Character > 0)
		})
	}
}

func TestUnmarshalOrdinaryAnchors(t *testing.T) {
	spec, err := LoadSpec([]byte(depsSpecHeader + `
args: &env {MESSAGE: &message hello}
sources:
  first: &source
    inline:
      file: {contents: *message}
  second: *source
build:
  env: *env
  steps:
    - &step {command: echo hello, env: *env}
    - *step
dependencies:
  build:
    first: &constraint {version: &versions [">= 1"]}
    second: *constraint
  runtime:
    third: {version: *versions}
`))
	assert.NilError(t, err)
	assert.Equal(t, spec.Sources["second"].Inline.File.Contents, "hello")
	assert.Equal(t, spec.Build.Steps[1].Env["MESSAGE"], "hello")
	assert.DeepEqual(t, spec.Dependencies.Runtime["third"].Version, []string{">= 1"})
	assert.DeepEqual(t, spec.Dependencies.Build["second"].Version, []string{">= 1"})
}

func TestUnmarshalTaggedAliases(t *testing.T) {
	spec, err := LoadSpec([]byte(depsSpecHeader + `
x-first: &first hello
x-second: &second !!str *first
sources:
  src: &source
    inline:
      file:
        contents: *second
`))
	assert.NilError(t, err)
	assert.Equal(t, spec.Sources["src"].Inline.File.Contents, "hello")
}

func TestUnmarshalMergePrecedence(t *testing.T) {
	spec, err := LoadSpec([]byte(depsSpecHeader + `
x-first: &first
  merged: {version: [first], arch: [amd64]}
  explicit: {version: [first]}
x-chain: &chain {<<: *first}
dependencies:
  build:
    <<: *chain
    explicit: {version: [explicit]}
`))
	assert.NilError(t, err)
	assert.DeepEqual(t, spec.Dependencies.Build["merged"].Version, []string{"first"})
	assert.DeepEqual(t, spec.Dependencies.Build["merged"].Arch, []string{"amd64"})
	assert.DeepEqual(t, spec.Dependencies.Build["explicit"].Version, []string{"explicit"})
}

func TestUnmarshalSourceVariants(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"context", "context: {name: *value}"},
		{"image", "image: {ref: *value}"},
		{"git", "git: {url: *value, commit: main}"},
		{"http", "http: {url: *value}"},
		{"build", "build: {source: {context: {name: *value}}}"},
		{"inline", "inline: {file: {contents: *value}}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Decode without validation: these values exercise decoding, not
			// network URLs or image reference validation.
			var spec Spec
			err := yaml.Unmarshal([]byte("x-value: &value test\nsources:\n  src:\n    "+tc.body+"\n"), &spec)
			assert.NilError(t, err)
			src := spec.Sources["src"]
			switch tc.name {
			case "context":
				assert.Equal(t, src.Context.Name, "test")
			case "image":
				assert.Equal(t, src.DockerImage.Ref, "test")
			case "git":
				assert.Equal(t, src.Git.URL, "test")
			case "http":
				assert.Equal(t, src.HTTP.URL, "test")
			case "build":
				assert.Equal(t, src.Build.Source.Context.Name, "test")
			case "inline":
				assert.Equal(t, src.Inline.File.Contents, "test")
			}
		})
	}
}

func TestUnmarshalOptionIsolation(t *testing.T) {
	for _, body := range []string{
		"sources:\n  src:\n    inline:\n      file: {contents: hi, unknown: true}\n",
		"build:\n  steps:\n    - {command: echo hi, x-private: true}\n",
		"dependencies:\n  build:\n    pkg: {version: [one], unknown: true}\n",
		"tests:\n  - name: check\n    files:\n      /file: {equals: hi, unknown: true}\n",
	} {
		t.Run(strings.Split(body, ":")[0], func(t *testing.T) {
			dt := []byte(depsSpecHeader + "x-allowed: {anything: true}\n" + body)
			var permissive Spec
			assert.NilError(t, yaml.Unmarshal(dt, &permissive))

			_, err := LoadSpec(dt)
			assert.ErrorContains(t, err, "unknown field")
			assert.ErrorContains(t, err, "error unmarshalling spec")

			// Strict options belong to this spec, not the next decoder.
			strict := Spec{decodeOpts: []yaml.DecodeOption{yaml.Strict()}}
			assert.ErrorContains(t, yaml.Unmarshal(dt, &strict), "unknown field")
			var next Spec
			assert.NilError(t, yaml.Unmarshal(dt, &next))
		})
	}
}

func TestUnmarshalErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"malformed", "sources: [", "error unmarshalling spec"},
		{"missing alias", "sources:\n  src: *missing\n", "could not find alias"},
		{"constraint shape", "dependencies:\n  build:\n    pkg: {version: {bad: shape}}\n", "unmarshal package constraints"},
		{"inline shape", "sources:\n  src:\n    inline:\n      file: {uid: [1, 2]}\n", "cannot unmarshal"},
		{"step shape", "build:\n  steps: {command: echo hi}\n", "expected sequence node"},
		{"output shape", "tests:\n  - name: check\n    steps:\n      - command: echo hi\n        stdout: {contains: {bad: shape}}\n", "error unmarshalling check output"},
		// These are existing strict-mode limits, not new merge semantics.
		{"step override", "x-step: &step {command: echo hi}\nbuild:\n  steps:\n    - <<: *step\n      command: echo override\n", `duplicate key "command"`},
		{"merge sequence", "x-c: &c {version: [one]}\ndependencies:\n  build:\n    pkg: {<<: [*c]}\n", "sequence was used where mapping is expected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadSpec([]byte(depsSpecHeader + tc.body))
			assert.ErrorContains(t, err, tc.want)
			assert.ErrorContains(t, err, "error unmarshalling spec")
		})
	}
}

func TestUnmarshalIndependentLoads(t *testing.T) {
	load := func(t *testing.T, value string) {
		t.Helper()
		dt := []byte(depsSpecHeader + fmt.Sprintf(`
x-value: &value %s
sources:
  src:
    inline:
      file: {contents: *value}
`, value))
		spec, err := LoadSpec(dt)
		assert.NilError(t, err)
		assert.Equal(t, spec.Sources["src"].Inline.File.Contents, value)
	}

	for i := range 3 {
		load(t, fmt.Sprintf("sequential-%d", i))
	}
	_, err := LoadSpec([]byte("x-value: &value stale\nsources:\n  src: *missing\n"))
	assert.Assert(t, err != nil)
	load(t, "after-failure")

	for i := range 8 {
		t.Run(fmt.Sprintf("parallel-%d", i), func(t *testing.T) {
			t.Parallel()
			load(t, fmt.Sprintf("parallel-%d", i))
		})
	}
}

func TestUnmarshalStandalone(t *testing.T) {
	t.Run("source", func(t *testing.T) {
		var src Source
		assert.NilError(t, yaml.Unmarshal([]byte("inline:\n  file: {contents: hello, uid: 12, gid: 34}\n"), &src))
		assert.Equal(t, src.Inline.File.Contents, "hello")
		assert.Equal(t, src.Inline.File.UID, 12)
		assert.Equal(t, src.Inline.File.GID, 34)
	})
	t.Run("file check", func(t *testing.T) {
		var check FileCheckOutput
		assert.NilError(t, yaml.Unmarshal([]byte("permissions: 0644\nequals: hello\ncontains: [ell]\n"), &check))
		assert.Equal(t, check.Equals, "hello")
		assert.DeepEqual(t, check.Contains, []string{"ell"})
		assert.Equal(t, check.Permissions.String(), "-rw-r--r--")
	})
	t.Run("constraints", func(t *testing.T) {
		var constraints PackageConstraints
		assert.NilError(t, yaml.Unmarshal([]byte("version: [one]\narch: [amd64]\n"), &constraints))
		assert.DeepEqual(t, constraints.Version, []string{"one"})
		assert.DeepEqual(t, constraints.Arch, []string{"amd64"})
	})
}

func TestUnmarshalExtensionAliases(t *testing.T) {
	dt := []byte(depsSpecHeader + `
x-scalar: &scalar hello
x-map: &mapping {value: *scalar}
x-sequence: &sequence [*scalar, world]
x-chain: {<<: *mapping, other: *sequence}
sources:
  src:
    inline:
      file: {contents: *scalar}
`)
	spec, err := LoadSpec(dt)
	assert.NilError(t, err)
	assert.Equal(t, spec.Sources["src"].Inline.File.Contents, "hello")
	checkExt(t, *spec, "x-scalar", "hello")
	// Ext decodes the isolated extension node, not the entire document.
	// Cross-extension references currently cannot be resolved by Ext.
	var mapping map[string]string
	assert.ErrorContains(t, spec.Ext("x-map", &mapping), "could not find alias")
	var sequence []string
	assert.ErrorContains(t, spec.Ext("x-sequence", &sequence), "could not find alias")
	var chain struct {
		Value string
		Other []string
	}
	assert.ErrorContains(t, spec.Ext("x-chain", &chain), "alias")

	// Marshaling sorts extension keys, so a merge can precede its anchor.
	// Such cross-extension merges do not currently round-trip.
	dt, err = yaml.Marshal(spec)
	assert.NilError(t, err)
	_, err = LoadSpec(dt)
	assert.ErrorContains(t, err, "cannot find anchor")
}

func TestUnmarshalExtensionRoundtrip(t *testing.T) {
	dt := []byte(depsSpecHeader + `
x-scalar: &scalar hello
x-map: {first: &local hello, second: *local}
x-sequence: [&item hello, *item]
sources:
  src:
    inline:
      file: {contents: *scalar}
`)
	for range 2 {
		spec, err := LoadSpec(dt)
		assert.NilError(t, err)
		assert.Equal(t, spec.Sources["src"].Inline.File.Contents, "hello")
		checkExt(t, *spec, "x-map", map[string]string{"first": "hello", "second": "hello"})
		checkExt(t, *spec, "x-sequence", []string{"hello", "hello"})
		dt, err = yaml.Marshal(spec)
		assert.NilError(t, err)
	}
}

func TestNodeDecoderReuse(t *testing.T) {
	base := parseDecodeNode(t, "x-value: &value hello\n")
	child := parseDecodeNode(t, "value: *value\n")
	invalid := parseDecodeNode(t, "x-invalid: {<<: *missing}\n")
	ctx := withBaseDoc(context.Background(), base)
	dec := getDecoder(ctx)

	for range 2 {
		var dst map[string]string
		assert.NilError(t, dec.DecodeFromNodeContext(ctx, child, &dst))
		assert.Equal(t, dst["value"], "hello")

		// If another pass reinitializes anchors, it will now fail. Mutation
		// here is only a probe for initialization, not how callers use the AST.
		base.(*ast.MappingNode).Values = invalid.(*ast.MappingNode).Values
	}
}

func TestNodeDecoderInitializationError(t *testing.T) {
	base := parseDecodeNode(t, "x-invalid: {<<: *missing}\n")
	child := parseDecodeNode(t, "value: hello\n")
	ctx := withBaseDoc(context.Background(), base)
	dec := getDecoder(ctx)

	for range 2 {
		var dst map[string]string
		err := dec.DecodeFromNodeContext(ctx, child, &dst)
		assert.ErrorContains(t, err, "cannot find anchor")
		assert.Assert(t, dst == nil)

		// Even if the base becomes valid, retain the initialization error
		// rather than decoding with partially initialized anchor state.
		base.(*ast.MappingNode).Values = child.(*ast.MappingNode).Values
	}
}

func TestUnmarshalOriginalAliasLocations(t *testing.T) {
	// Blank lines, comments, and a block scalar must not be normalized away
	// when resolving anchors. In particular, keep the end of the range too.
	doc := depsSpecHeader + `
# Keep these gaps in the original source map.

x-file: &file
  contents: |
    hello
    world
  uid: &uid 123
  gid: 456

x-constraint: &constraint
  version: [">= 1"]
  arch: [amd64]

sources:
  src:
    inline:
      file: *file
dependencies:
  build:
    pkg: *constraint
`
	spec, err := LoadSpecWithSourceMap("original.yml", []byte(doc))
	assert.NilError(t, err)

	file := spec.Sources["src"].Inline.File
	assert.Equal(t, file.Contents, "hello\nworld\n")
	for _, tc := range []struct {
		sm         *sourceMap
		start, end string
		column     int32
		endColumn  int32
	}{
		{file._sourceMap, "contents: |", "gid: 456", 11, 8},
		{spec.Dependencies.Build["pkg"]._sourceMap, `version: [">= 1"]`, "arch: [amd64]", 10, 15},
	} {
		assert.Assert(t, tc.sm != nil)
		assert.Equal(t, tc.sm.filename, "original.yml")
		assert.Equal(t, tc.sm.language, "yaml")
		assert.DeepEqual(t, tc.sm.data, []byte(doc))
		assert.Equal(t, tc.sm.pos.Start.Line, int32(lineOf(doc, tc.start)))
		assert.Equal(t, tc.sm.pos.Start.Character, tc.column)
		assert.Equal(t, tc.sm.pos.End.Line, int32(lineOf(doc, tc.end)))
		assert.Equal(t, tc.sm.pos.End.Character, tc.endColumn)
	}
}

func TestUnmarshalLargeDocument(t *testing.T) {
	const count = 64
	dt := syntheticDecodeSpec(count, 64*1024)
	spec, err := LoadSpecWithSourceMap("large.yml", dt)
	assert.NilError(t, err)
	assert.Equal(t, len(spec.Sources), count)
	assert.Equal(t, len(spec.Build.Steps), count)
	assert.Equal(t, len(spec.Dependencies.Build), count)

	for i := range count {
		key := fmt.Sprintf("item%d", i)
		assert.Equal(t, spec.Sources[key].Inline.File.Contents, "hello")
		assert.Equal(t, spec.Sources[key].Inline.File.UID, 123)
		assert.Equal(t, spec.Build.Steps[i].Command, "echo hello")
		assert.DeepEqual(t, spec.Dependencies.Build[key].Version, []string{">= 1"})
		assert.Equal(t, spec.Dependencies.Build[key]._sourceMap.filename, "large.yml")
	}
}

func parseDecodeNode(t *testing.T, doc string) ast.Node {
	t.Helper()
	file, err := parser.ParseBytes([]byte(doc), 0)
	assert.NilError(t, err)
	return file.Docs[0].Body
}

// Vary padding independently of the number of custom unmarshaler calls. This
// exposes whole-document work even when most of the document is inline content.
func syntheticDecodeSpec(count, padding int) []byte {
	var doc strings.Builder
	doc.WriteString(depsSpecHeader)
	doc.WriteString("x-padding: " + strings.Repeat("a", padding) + "\n")
	doc.WriteString(`x-file: &file {contents: hello, uid: 123, gid: 456}
x-source: &source {inline: {file: *file}}
x-step: &step {command: echo hello}
x-constraint: &constraint {version: [">= 1"]}
sources:
`)
	for i := range count {
		fmt.Fprintf(&doc, "  item%d: *source\n", i)
	}
	doc.WriteString("build:\n  steps:\n")
	for range count {
		doc.WriteString("    - *step\n")
	}
	doc.WriteString("dependencies:\n  build:\n")
	for i := range count {
		fmt.Fprintf(&doc, "    item%d: *constraint\n", i)
	}
	return []byte(doc.String())
}
