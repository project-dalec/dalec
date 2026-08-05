package deb

import (
	"bytes"
	"testing"

	"github.com/project-dalec/dalec"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestWritePackagePerms(t *testing.T) {
	spec := &dalec.Spec{
		Sources: map[string]dalec.Source{
			"empty-inline-file": {
				Inline: &dalec.SourceInline{File: &dalec.SourceInlineFile{}},
			},
			"inline-file": {
				Inline: &dalec.SourceInline{
					File: &dalec.SourceInlineFile{Permissions: 0o600},
				},
			},
			"inline-tree": {
				Inline: &dalec.SourceInline{
					Dir: &dalec.SourceInlineDir{
						Permissions: 0o750,
						Files: map[string]*dalec.SourceInlineFile{
							"child": {Permissions: 0o640},
						},
					},
				},
			},
		},
	}
	pkg := resolvedPackage{
		name: "example-tools",
		artifacts: dalec.Artifacts{
			Binaries: map[string]dalec.ArtifactConfig{
				"empty-inline-file": {},
				"explicit":          {Name: "renamed", SubPath: "tools", Permissions: 0o711},
				"inline-file":       {Name: "file-copy"},
				"inline-tree":       {Name: "tree-copy"},
				"inline-tree/child": {SubPath: "children"},
				"missing-source":    {},
			},
		},
	}

	buf := bytes.NewBuffer(nil)
	writePackagePerms(buf, spec, pkg)
	content := buf.String()

	tests := []struct {
		name     string
		expected string
	}{
		{
			name:     "explicit artifact permissions take precedence",
			expected: "chmod 711 \"debian/example-tools/usr/bin/tools/renamed\"\n",
		},
		{
			name:     "inline file permissions are retained",
			expected: "chmod 600 \"debian/example-tools/usr/bin/file-copy\"\n",
		},
		{
			name:     "inline directory permissions are retained",
			expected: "chmod 750 \"debian/example-tools/usr/bin/tree-copy\"\n",
		},
		{
			name:     "inline directory child permissions are retained",
			expected: "chmod 640 \"debian/example-tools/usr/bin/children/child\"\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Assert(t, cmp.Contains(content, test.expected))
		})
	}

	t.Run("artifacts without explicit or inline permissions do not emit chmod", func(t *testing.T) {
		assert.Assert(t, !bytes.Contains(buf.Bytes(), []byte("empty-inline-file")))
		assert.Assert(t, !bytes.Contains(buf.Bytes(), []byte("missing-source")))
	})
}
