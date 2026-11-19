package test

import (
	"archive/zip"
	"bytes"
	"context"
	"debug/macho"
	"fmt"
	"io"
	"io/fs"
	"testing"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/project-dalec/dalec"
	"gotest.tools/v3/assert"
)

func TestDarwin(t *testing.T) {
	t.Parallel()
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			t.Parallel()
			ctx := startTestSpan(baseCtx, t)
			spec := fillMetadata("darwin-hello", &dalec.Spec{
				Args: map[string]string{"TARGETARCH": ""},
				Sources: map[string]dalec.Source{
					"main.go": {Inline: &dalec.SourceInline{File: &dalec.SourceInlineFile{
						Contents: "package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"hello\") }\n",
					}}},
					"LICENSE": {Inline: &dalec.SourceInline{File: &dalec.SourceInlineFile{Contents: "test license\n"}}},
				},
				Dependencies: &dalec.PackageDependencies{
					Build: dalec.PackageDependencyList{"golang-go": {}},
				},
				Build: dalec.ArtifactBuild{
					Env:   map[string]string{"GOARCH": "${TARGETARCH}", "CGO_ENABLED": "0"},
					Steps: dalec.BuildStepList{{Command: "go build -o hello main.go"}},
				},
				Artifacts: dalec.Artifacts{
					Binaries: map[string]dalec.ArtifactConfig{"hello": {Permissions: 0o755}},
					Licenses: map[string]dalec.ArtifactConfig{"LICENSE": {}},
				},
			})
			testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
				res := solveT(ctx, t, gwc, newSolveRequest(
					withSpec(ctx, t, spec), withBuildTarget("darwincross/zip"),
					withPlatform(ocispecs.Platform{OS: "darwin", Architecture: arch}),
				))
				ref, err := res.SingleRef()
				assert.NilError(t, err)
				filename := fmt.Sprintf("/%s_%s-%s_darwin_%s.zip", spec.Name, spec.Version, spec.Revision, arch)
				data, err := ref.ReadFile(ctx, gwclient.ReadRequest{Filename: filename})
				assert.NilError(t, err)
				archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
				assert.NilError(t, err)
				files := make(map[string][]byte)
				for _, file := range archive.File {
					if file.FileInfo().IsDir() {
						continue
					}
					reader, err := file.Open()
					assert.NilError(t, err)
					content, err := io.ReadAll(reader)
					assert.NilError(t, err)
					assert.NilError(t, reader.Close())
					files[file.Name] = content
					if file.Name == "hello" {
						assert.Equal(t, file.Mode().Perm(), fs.FileMode(0o755))
					}
				}
				assert.Equal(t, string(files["licenses/LICENSE"]), "test license\n")
				binary, err := macho.NewFile(bytes.NewReader(files["hello"]))
				assert.NilError(t, err)
				defer binary.Close()
				expected := macho.CpuAmd64
				if arch == "arm64" {
					expected = macho.CpuArm64
				}
				assert.Equal(t, binary.Cpu, expected)
			})
		})
	}
}
