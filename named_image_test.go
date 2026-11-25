package dalec

import (
	"encoding/json"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestNamedImageLoading(t *testing.T) {
	const input = `
name: foo
image:
  entrypoint: /default
  env: [DEFAULT=1]
targets:
  azlinux3:
    image:
      user: default-user
    packages:
      tools:
        name: custom-tools
        description: tools
    images:
      minimal:
        packages: [custom-tools]
        base: example.com/base:1
        entrypoint: /minimal
        labels:
          version: ${VERSION}
        tests:
          - name: image test
            env:
              VERSION: ${VERSION}
            steps:
              - command: echo ${VERSION}
args:
  VERSION: "1"
`
	spec, err := LoadSpec([]byte(input))
	assert.NilError(t, err)
	assert.NilError(t, spec.SubstituteArgs(nil))
	selected, err := spec.WithNamedImage("azlinux3", "minimal")
	assert.NilError(t, err)
	assert.Equal(t, selected.Image.Entrypoint, "/minimal")
	assert.Equal(t, selected.Image.User, "")
	assert.Equal(t, len(selected.Image.Env), 0)
	assert.Equal(t, selected.GetImageBases("azlinux3")[0].Rootfs.DockerImage.Ref, "example.com/base:1")
	assert.Equal(t, selected.Image.Labels["version"], "1")
	assert.Equal(t, selected.Targets["azlinux3"].Tests[0].Env["VERSION"], "1")
	assert.DeepEqual(t, selected.GetImagePackages(), []string{"custom-tools"})
	assert.Equal(t, spec.Image.Entrypoint, "/default")
	assert.Equal(t, spec.Targets["azlinux3"].Image.User, "default-user")
	assert.Equal(t, len(spec.Targets["azlinux3"].Tests), 0)

	data, err := json.Marshal(spec.Targets["azlinux3"].Images["minimal"])
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(string(data), `"entrypoint":"/minimal"`))
	assert.Assert(t, !strings.Contains(string(data), `"ImageConfig"`))
}

func TestNamedImageValidation(t *testing.T) {
	for _, tc := range []struct {
		name, definition, want string
	}{
		{"omitted selection", "minimal: {}", ""},
		{"empty selection", "minimal: {packages: []}", "packages must not be empty"},
		{"primary", "minimal: {packages: [foo]}", ""},
		{"resolved supplemental", "minimal: {packages: [custom-tools]}", ""},
		{"unresolved supplemental key", "minimal: {packages: [tools]}", `unknown package "tools"`},
		{"invalid name", "'bad/name': {}", "invalid image name"},
		{"invalid base", "minimal: {base: one, bases: [{rootfs: {image: {ref: two}}}]}", "cannot specify both"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadSpec([]byte("name: foo\ntargets:\n  azlinux3:\n    packages:\n      tools:\n        name: custom-tools\n        description: tools\n    images:\n      " + tc.definition + "\n"))
			if tc.want == "" {
				assert.NilError(t, err)
			} else {
				assert.ErrorContains(t, err, tc.want)
			}
		})
	}
}

func TestNamedImageTestComposition(t *testing.T) {
	rootTest, targetTest, imageTest := &TestSpec{Name: "root"}, &TestSpec{Name: "target"}, &TestSpec{Name: "image"}
	spec := &Spec{
		Tests: []*TestSpec{rootTest},
		Targets: map[string]Target{
			"distro": {
				Tests: []*TestSpec{targetTest},
				Images: map[string]NamedImage{
					"minimal": {Tests: []*TestSpec{imageTest}},
					"all":     {},
				},
			},
		},
	}
	selected, err := spec.WithNamedImage("distro", "minimal")
	assert.NilError(t, err)
	assert.Equal(t, len(selected.Tests), 1)
	assert.Assert(t, selected.Tests[0] == rootTest)
	assert.Equal(t, len(selected.Targets["distro"].Tests), 2)
	assert.Assert(t, selected.Targets["distro"].Tests[0] == targetTest)
	assert.Assert(t, selected.Targets["distro"].Tests[1] == imageTest)
	assert.Equal(t, len(spec.Targets["distro"].Tests), 1)
	all, err := spec.WithNamedImage("distro", "all")
	assert.NilError(t, err)
	assert.Assert(t, cmp.Nil(all.GetImagePackages()))
	original, err := spec.WithNamedImage("distro", "")
	assert.NilError(t, err)
	assert.Assert(t, original == spec)
	_, err = spec.WithNamedImage("distro", "missing")
	assert.ErrorContains(t, err, `unknown image "missing"`)
}
