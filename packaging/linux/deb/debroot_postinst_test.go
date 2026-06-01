package deb

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/solver/pb"
	"github.com/project-dalec/dalec"
	"gotest.tools/v3/assert"
)

func TestDebrootPostinstIncludesDebhelperMarker(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	spec := &dalec.Spec{
		Name:        "example",
		Description: "Example package",
		Website:     "https://example.invalid",
		Version:     "1.0.0",
		Revision:    "1",
		License:     "Apache-2.0",
		Artifacts: dalec.Artifacts{
			Users: []dalec.AddUserConfig{
				{Name: "dalecuser"},
			},
		},
	}

	st := Debroot(ctx, dalec.SourceOpts{}, spec, llb.Scratch(), llb.Scratch(), "", "", "", SourcePkgConfig{})
	def, err := st.Marshal(ctx)
	assert.NilError(t, err)

	mkfile, err := findMkfile(t, def.ToPB(), filepath.Join("/debian", "postinst"))
	assert.NilError(t, err)
	assert.Assert(t, mkfile != nil)

	assert.Equal(t, int32(0o700), mkfile.Mode)
	assert.Assert(t, bytes.Contains(mkfile.Data, []byte("#DEBHELPER#")))
	assert.Assert(t, bytes.Contains(mkfile.Data, []byte("useradd dalecuser")))
}

func findMkfile(t *testing.T, def *pb.Definition, path string) (*pb.FileActionMkFile, error) {
	for _, dt := range def.Def {
		var op pb.Op
		if err := op.Unmarshal(dt); err != nil {
			return nil, err
		}

		fileOp := op.GetFile()
		if fileOp == nil {
			continue
		}

		for _, action := range fileOp.Actions {
			mkfile := action.GetMkfile()
			if mkfile == nil {
				continue
			}

			t.Log(mkfile.Path)
			if filepath.Clean(mkfile.Path) == filepath.Clean(path) {
				return mkfile, nil
			}
		}
	}

	return nil, nil
}
