package windows

import (
	"strings"
	"testing"

	"github.com/project-dalec/dalec"
	"gotest.tools/v3/assert"
)

const testTargetKey = "windowscross"

func subpackageSpec() *dalec.Spec {
	return &dalec.Spec{
		Name:    "foo",
		Version: "1.0.0",
		Artifacts: dalec.Artifacts{
			Binaries: map[string]dalec.ArtifactConfig{
				"bin/foo.exe": {},
			},
		},
		Targets: map[string]dalec.Target{
			testTargetKey: {
				Packages: map[string]dalec.SubPackage{
					"contrib": {
						Description: "Contrib extras for foo",
						Artifacts: &dalec.Artifacts{
							Binaries: map[string]dalec.ArtifactConfig{
								"bin/foo-contrib.exe": {},
							},
						},
					},
					"tools": {
						Name:        "foo-utilities",
						Description: "Foo utilities",
						Artifacts: &dalec.Artifacts{
							Binaries: map[string]dalec.ArtifactConfig{
								"bin/util.exe": {Name: "foo-util.exe"},
							},
						},
					},
				},
			},
		},
	}
}

func TestWindowsPackages(t *testing.T) {
	spec := subpackageSpec()
	pkgs := windowsPackages(spec, testTargetKey)

	// Primary is always first; supplemental packages follow sorted by map key
	// ("contrib" before "tools").
	assert.Equal(t, len(pkgs), 3)
	assert.Equal(t, pkgs[0].Name, "foo")
	assert.Equal(t, pkgs[1].Name, "foo-contrib")
	// "tools" has a name override.
	assert.Equal(t, pkgs[2].Name, "foo-utilities")

	assert.Equal(t, len(pkgs[0].Binaries), 1)
	assert.Equal(t, len(pkgs[2].Binaries), 1)
}

func TestGenerateInvocationScript(t *testing.T) {
	spec := subpackageSpec()
	script := generateInvocationScript(spec, testTargetKey).String()

	// The primary package stages at the root of the output dir; supplemental
	// packages each get their own subdirectory.
	assert.Assert(t, strings.Contains(script, "mkdir -p '"+outputDir+"'"))
	assert.Assert(t, strings.Contains(script, "mkdir -p '"+outputDir+"/foo-contrib'"))
	assert.Assert(t, strings.Contains(script, "mkdir -p '"+outputDir+"/foo-utilities'"))

	// Files are copied (not moved) into the package's staging directory by their
	// resolved name. The primary package's files land at the output dir root.
	assert.Assert(t, strings.Contains(script, "cp -r 'bin/foo.exe' '"+outputDir+"/foo.exe'"))
	assert.Assert(t, strings.Contains(script, "cp -r 'bin/foo-contrib.exe' '"+outputDir+"/foo-contrib/foo-contrib.exe'"))
	// Name override on the artifact is honored.
	assert.Assert(t, strings.Contains(script, "cp -r 'bin/util.exe' '"+outputDir+"/foo-utilities/foo-util.exe'"))

	// The build script is still invoked.
	assert.Assert(t, strings.Contains(script, buildScriptName))
}

func TestGenerateInvocationScriptPermissions(t *testing.T) {
	spec := &dalec.Spec{
		Name: "foo",
		Artifacts: dalec.Artifacts{
			Binaries: map[string]dalec.ArtifactConfig{
				"sub/dir/foo.exe": {Permissions: 0o755},
			},
		},
	}

	script := generateInvocationScript(spec, testTargetKey).String()

	// chmod must target the staged file (by resolved name), not the original
	// build path. This guards against the previous bug where chmod referenced
	// the source path under the output dir. The primary package stages at the
	// output dir root.
	assert.Assert(t, strings.Contains(script, "cp -r 'sub/dir/foo.exe' '"+outputDir+"/foo.exe'"))
	assert.Assert(t, strings.Contains(script, "chmod 755 '"+outputDir+"/foo.exe'"))
}

func TestValidateZipArtifacts(t *testing.T) {
	t.Run("all packages have artifacts", func(t *testing.T) {
		spec := subpackageSpec()
		assert.NilError(t, validateZipArtifacts(spec, testTargetKey))
	})

	t.Run("empty subpackage", func(t *testing.T) {
		spec := subpackageSpec()
		tgt := spec.Targets[testTargetKey]
		tgt.Packages["empty"] = dalec.SubPackage{Description: "no artifacts"}
		spec.Targets[testTargetKey] = tgt

		err := validateZipArtifacts(spec, testTargetKey)
		assert.ErrorContains(t, err, "foo-empty")
		assert.ErrorContains(t, err, "no artifacts")
	})

	t.Run("empty primary", func(t *testing.T) {
		spec := &dalec.Spec{Name: "foo"}
		err := validateZipArtifacts(spec, testTargetKey)
		assert.ErrorContains(t, err, "\"foo\"")
	})
}
