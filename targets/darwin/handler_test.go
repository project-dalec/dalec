package darwin

import (
	"strings"
	"testing"

	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/project-dalec/dalec"
	"gotest.tools/v3/assert"
)

func testSpec() *dalec.Spec {
	return &dalec.Spec{
		Name: "hello",
		Artifacts: dalec.Artifacts{
			Binaries: map[string]dalec.ArtifactConfig{
				"bin/hello": {Name: "hello-world", Permissions: 0o755},
			},
			Licenses: map[string]dalec.ArtifactConfig{"LICENSE": {}},
			Docs: map[string]dalec.ArtifactConfig{
				"README": {SubPath: "guide"},
			},
		},
	}
}

func TestValidate(t *testing.T) {
	for _, platform := range []ocispecs.Platform{
		{OS: "darwin", Architecture: "amd64"},
		{OS: "darwin", Architecture: "arm64"},
	} {
		assert.NilError(t, validate(testSpec(), DefaultTargetKey, &platform))
	}
	for _, platform := range []*ocispecs.Platform{
		nil,
		{OS: "linux", Architecture: "amd64"},
		{OS: "windows", Architecture: "amd64"},
		{OS: "darwin", Architecture: "386"},
	} {
		assert.ErrorContains(t, validate(testSpec(), DefaultTargetKey, platform), "requires darwin/amd64 or darwin/arm64")
	}
	spec := testSpec()
	spec.Artifacts.Binaries = nil
	assert.ErrorContains(t, validate(spec, DefaultTargetKey, &ocispecs.Platform{OS: "darwin", Architecture: "arm64"}), `package "hello" produces no binaries`)
}

func TestInvocationScript(t *testing.T) {
	spec := testSpec()
	spec.Targets = map[string]dalec.Target{
		DefaultTargetKey: {
			Packages: map[string]dalec.SubPackage{
				"extra": {
					Artifacts: &dalec.Artifacts{
						Binaries: map[string]dalec.ArtifactConfig{"bin/extra": {}},
					},
				},
			},
		},
	}
	pkgs := packages(spec, DefaultTargetKey)
	script, outputs := invocationScript(pkgs)
	assert.DeepEqual(t, outputs, []string{"/tmp/output/0", "/tmp/output/1"})
	assert.Equal(t, pkgs[1].name, "hello-extra")
	for _, command := range []string{
		"/tmp/scripts/_build.sh",
		"cp -r 'bin/hello' '/tmp/output/0/hello-world'",
		"chmod 755 '/tmp/output/0/hello-world'",
		"cp -r 'LICENSE' '/tmp/output/0/licenses/LICENSE'",
		"mkdir -p '/tmp/output/0/docs/guide'",
		"cp -r 'README' '/tmp/output/0/docs/guide/README'",
		"cp -r 'bin/extra' '/tmp/output/1/extra'",
	} {
		assert.Assert(t, strings.Contains(script, command), "missing %q in:\n%s", command, script)
	}
	spec.Targets[DefaultTargetKey].Packages["empty"] = dalec.SubPackage{}
	assert.ErrorContains(t, validate(spec, DefaultTargetKey, &ocispecs.Platform{OS: "darwin", Architecture: "arm64"}), `package "hello-empty" produces no binaries`)
}
