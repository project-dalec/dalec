package deb

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/dalec"
	"github.com/moby/buildkit/client/llb"
)

func TestCreatePatchScriptIncludesGomodPatches(t *testing.T) {
	spec := &dalec.Spec{
		Sources: map[string]dalec.Source{
			"src": {
				Inline: &dalec.SourceInline{Dir: &dalec.SourceInlineDir{}},
			},
		},
	}

	patch := llb.Scratch().File(llb.Mkfile("gomod.patch", 0o644, []byte("patch")))
	spec.AddGomodPatchForTesting(&dalec.GomodPatch{
		SourceName: "src",
		FileName:   "gomod.patch",
		Strip:      1,
		State:      patch,
		Contents:   []byte("patch"),
	})

	script := string(createPatchScript(spec, nil))

	gomodPatchPath := filepath.Join("${DEBIAN_DIR:=debian}/dalec/patches", "src", "gomod.patch")
	expected := fmt.Sprintf("if [ -s %q ]; then patch -N -d %q -p%d -s < %q; fi", gomodPatchPath, "src", 1, gomodPatchPath)

	if !strings.Contains(script, expected) {
		t.Fatalf("expected gomod patch application command %q in script:\n%s", expected, script)
	}
}
