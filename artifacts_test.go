package dalec

import (
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestArtifactsBuildArgSubstitution(t *testing.T) {
	t.Parallel()

	t.Run("artifact paths are substituted", func(t *testing.T) {
		t.Parallel()

		dt := []byte(`
args:
  TARGETARCH:
  OUT_DIR: out

artifacts:
  binaries:
    ${OUT_DIR}/${TARGETARCH}/bin/foo: {}
  libexec:
    ${OUT_DIR}/${TARGETARCH}/libexec/foo-helper: {}
  opt:
    ${OUT_DIR}/${TARGETARCH}/opt/foo: {}
  manpages:
    ${OUT_DIR}/man/foo.1: {}
  data_dirs:
    ${OUT_DIR}/share/foo: {}
  configFiles:
    ${OUT_DIR}/etc/foo.conf: {}
  docs:
    ${OUT_DIR}/README.md: {}
  licenses:
    ${OUT_DIR}/LICENSE: {}
  libs:
    ${OUT_DIR}/${TARGETARCH}/lib/libfoo.so: {}
  headers:
    ${OUT_DIR}/include/foo.h: {}
  systemd:
    units:
      ${OUT_DIR}/systemd/foo.service: {}
    dropins:
      ${OUT_DIR}/systemd/foo.conf:
        unit: foo.service

targets:
  azlinux3:
    artifacts:
      binaries:
        ${OUT_DIR}/${TARGETARCH}/bin/bar: {}
    packages:
      tools:
        description: Tools package
        artifacts:
          binaries:
            ${OUT_DIR}/${TARGETARCH}/bin/baz: {}
`)

		spec, err := LoadSpec(dt)
		assert.NilError(t, err)

		err = spec.SubstituteArgs(map[string]string{"TARGETARCH": "arm64"})
		assert.NilError(t, err)

		assert.Check(t, cmp.DeepEqual(spec.Artifacts.Binaries, map[string]ArtifactConfig{"out/arm64/bin/foo": {}}))
		assert.Check(t, cmp.DeepEqual(spec.Artifacts.Libexec, map[string]ArtifactConfig{"out/arm64/libexec/foo-helper": {}}))
		assert.Check(t, cmp.DeepEqual(spec.Artifacts.Opt, map[string]ArtifactConfig{"out/arm64/opt/foo": {}}))
		assert.Check(t, cmp.DeepEqual(spec.Artifacts.Manpages, map[string]ArtifactConfig{"out/man/foo.1": {}}))
		assert.Check(t, cmp.DeepEqual(spec.Artifacts.DataDirs, map[string]ArtifactConfig{"out/share/foo": {}}))
		assert.Check(t, cmp.DeepEqual(spec.Artifacts.ConfigFiles, map[string]ArtifactConfig{"out/etc/foo.conf": {}}))
		assert.Check(t, cmp.DeepEqual(spec.Artifacts.Docs, map[string]ArtifactConfig{"out/README.md": {}}))
		assert.Check(t, cmp.DeepEqual(spec.Artifacts.Licenses, map[string]ArtifactConfig{"out/LICENSE": {}}))
		assert.Check(t, cmp.DeepEqual(spec.Artifacts.Libs, map[string]ArtifactConfig{"out/arm64/lib/libfoo.so": {}}))
		assert.Check(t, cmp.DeepEqual(spec.Artifacts.Headers, map[string]ArtifactConfig{"out/include/foo.h": {}}))
		assert.Check(t, cmp.DeepEqual(spec.Artifacts.Systemd.Units, map[string]SystemdUnitConfig{"out/systemd/foo.service": {}}))
		assert.Check(t, cmp.DeepEqual(spec.Artifacts.Systemd.Dropins, map[string]SystemdDropinConfig{"out/systemd/foo.conf": {Unit: "foo.service"}}))

		target := spec.Targets["azlinux3"]
		assert.Check(t, cmp.DeepEqual(target.Artifacts.Binaries, map[string]ArtifactConfig{"out/arm64/bin/bar": {}}))
		assert.Check(t, cmp.DeepEqual(target.Packages["tools"].Artifacts.Binaries, map[string]ArtifactConfig{"out/arm64/bin/baz": {}}))
	})

	t.Run("undeclared build arg in artifact path", func(t *testing.T) {
		t.Parallel()

		dt := []byte(`
args:

artifacts:
  binaries:
    out/${UNDECLARED}/foo: {}
`)

		spec, err := LoadSpec(dt)
		assert.NilError(t, err)

		err = spec.SubstituteArgs(map[string]string{})
		assert.ErrorContains(t, err, `artifacts: binaries: path "out/${UNDECLARED}/foo": error performing variable expansion: build arg "UNDECLARED" not declared`)
	})

	t.Run("opt-in platform arg not declared", func(t *testing.T) {
		t.Parallel()

		dt := []byte(`
targets:
  azlinux3:
    artifacts:
      libs:
        out/${TARGETARCH}/libfoo.so: {}
`)

		spec, err := LoadSpec(dt)
		assert.NilError(t, err)

		err = spec.SubstituteArgs(map[string]string{"TARGETARCH": "amd64"})
		assert.ErrorContains(t, err, `target azlinux3: artifacts: libs: path "out/${TARGETARCH}/libfoo.so"`)
		assert.ErrorContains(t, err, `opt-in arg "TARGETARCH" not present in args`)
	})

	t.Run("paths resolving to the same path", func(t *testing.T) {
		t.Parallel()

		dt := []byte(`
args:
  TARGETARCH:

artifacts:
  binaries:
    out/${TARGETARCH}/foo:
      subpath: a
    out/amd64/foo:
      subpath: b
`)

		spec, err := LoadSpec(dt)
		assert.NilError(t, err)

		err = spec.SubstituteArgs(map[string]string{"TARGETARCH": "amd64"})
		assert.ErrorContains(t, err, `artifacts: binaries: paths "out/${TARGETARCH}/foo" and "out/amd64/foo" both resolve to "out/amd64/foo"`)
	})

	t.Run("nil artifacts", func(t *testing.T) {
		t.Parallel()

		var a *Artifacts
		assert.NilError(t, a.processBuildArgs(nil, nil, DisallowAllUndeclared))
	})
}
