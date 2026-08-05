package deb

import (
	"maps"
	"reflect"
	"strings"
	"testing"

	"github.com/project-dalec/dalec"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestRules_OverrideSystemd(t *testing.T) {
	newWrapper := func(units map[string]dalec.SystemdUnitConfig) *rulesWrapper {
		return &rulesWrapper{
			Spec: &dalec.Spec{
				Artifacts: dalec.Artifacts{
					Systemd: &dalec.SystemdConfiguration{
						Units: units,
					},
				},
			},
		}
	}

	t.Run("no units", func(t *testing.T) {
		w := newWrapper(nil)
		out, err := w.OverrideSystemd()
		assert.NilError(t, err)
		expect := ""
		assert.Equal(t, out.String(), expect)
	})

	t.Run("single unit", func(t *testing.T) {
		t.Run("enabled", func(t *testing.T) {
			w := newWrapper(map[string]dalec.SystemdUnitConfig{
				"foo.service": {Enable: true},
			})

			out, err := w.OverrideSystemd()
			assert.NilError(t, err)
			expect := `override_dh_installsystemd:
	dh_installsystemd --name=foo
`
			assert.Equal(t, out.String(), expect)
		})

		t.Run("disabled", func(t *testing.T) {
			w := newWrapper(map[string]dalec.SystemdUnitConfig{
				"foo.service": {Enable: false},
			})

			out, err := w.OverrideSystemd()
			assert.NilError(t, err)
			expect := `override_dh_installsystemd:
	dh_installsystemd --name=foo --no-enable
`
			assert.Equal(t, out.String(), expect)
		})
	})

	t.Run("multiple units", func(t *testing.T) {
		t.Run("enabled", func(t *testing.T) {
			w := newWrapper(map[string]dalec.SystemdUnitConfig{
				"foo.service": {Enable: true},
				"foo.socket":  {Enable: true},
				"bar.service": {Enable: true},
			})

			out, err := w.OverrideSystemd()
			assert.NilError(t, err)
			expect := `override_dh_installsystemd:
	dh_installsystemd --name=bar
	dh_installsystemd --name=foo
`
			assert.Equal(t, out.String(), expect)
		})

		t.Run("disabled", func(t *testing.T) {
			w := newWrapper(map[string]dalec.SystemdUnitConfig{
				"foo.service": {Enable: false},
				"foo.socket":  {Enable: false},
				"bar.service": {Enable: false},
			})

			out, err := w.OverrideSystemd()
			assert.NilError(t, err)
			expect := `override_dh_installsystemd:
	dh_installsystemd --name=bar --no-enable
	dh_installsystemd --name=foo --no-enable
`
			assert.Equal(t, out.String(), expect)
		})

		t.Run("mixed", func(t *testing.T) {
			w := newWrapper(map[string]dalec.SystemdUnitConfig{
				"foo.service": {Enable: false},
				"foo.socket":  {Enable: true},
				"bar.service": {Enable: true},
			})

			out, err := w.OverrideSystemd()
			assert.NilError(t, err)
			expect := `override_dh_installsystemd:
	dh_installsystemd --name=bar
	dh_installsystemd --name=foo --no-enable
	[ -f debian/postinst ] || (echo '#!/bin/sh' > debian/postinst; echo 'set -e' >> debian/postinst)
	[ -x debian/postinst ] || chmod +x debian/postinst
	cat debian/dalec/custom_systemd_postinst.sh.partial >> debian/postinst
`
			assert.Equal(t, out.String(), expect)
		})
	})

	t.Run("a subpackage enables its service without enabling its socket", func(t *testing.T) {
		w := &rulesWrapper{
			Spec: &dalec.Spec{
				Name: "example",
				Targets: map[string]dalec.Target{
					"testdistro": {
						Packages: map[string]dalec.SubPackage{
							"svc": {
								Description: "Service package",
								Artifacts: &dalec.Artifacts{
									Systemd: &dalec.SystemdConfiguration{
										Units: map[string]dalec.SystemdUnitConfig{
											"foo.service": {Enable: true},
											"foo.socket":  {Enable: false},
										},
									},
								},
							},
						},
					},
				},
			},
			target: "testdistro",
		}

		out, err := w.OverrideSystemd()
		assert.NilError(t, err)
		expect := `override_dh_installsystemd:
	dh_installsystemd -pexample-svc --name=foo --no-enable
	[ -f debian/example-svc.postinst ] || (echo '#!/bin/sh' > debian/example-svc.postinst; echo 'set -e' >> debian/example-svc.postinst)
	[ -x debian/example-svc.postinst ] || chmod +x debian/example-svc.postinst
	cat debian/dalec/example-svc.custom_systemd_postinst.sh.partial >> debian/example-svc.postinst
`
		assert.Equal(t, out.String(), expect)
	})
}

func TestDepends(t *testing.T) {
	withDebhelperDependencies := `
Depends: ${misc:Depends},
         ${shlibs:Depends}`
	withRuntimeDependencies := `
Depends: ${misc:Depends},
         ${shlibs:Depends},
         bar,
         foo`

	tests := []struct {
		name                string
		runtimeDependencies dalec.PackageDependencyList
		disableAutoRequires bool
		expected            string
	}{
		{
			name:     "nil runtime dependencies add debhelper dependencies",
			expected: withDebhelperDependencies,
		},
		{
			name:                "empty runtime dependencies add debhelper dependencies",
			runtimeDependencies: dalec.PackageDependencyList{},
			expected:            withDebhelperDependencies,
		},
		{
			name: "package runtime dependencies are retained",
			runtimeDependencies: dalec.PackageDependencyList{
				"foo": {},
				"bar": {},
			},
			expected: withRuntimeDependencies,
		},
		{
			name: "existing shlibs dependency is not duplicated",
			runtimeDependencies: dalec.PackageDependencyList{
				"foo":               {},
				"bar":               {},
				"${shlibs:Depends}": {},
			},
			expected: withRuntimeDependencies,
		},
		{
			name: "existing debhelper dependencies are not duplicated",
			runtimeDependencies: dalec.PackageDependencyList{
				"foo":               {},
				"bar":               {},
				"${shlibs:Depends}": {},
				"${misc:Depends}":   {},
			},
			expected: withRuntimeDependencies,
		},
		{
			name:                "disabled automatic requirements omit shlibs dependency",
			disableAutoRequires: true,
			expected:            "Depends: ${misc:Depends}",
		},
		{
			name: "disabled automatic requirements retain explicit runtime dependencies",
			runtimeDependencies: dalec.PackageDependencyList{
				"foo": {},
				"bar": {},
			},
			disableAutoRequires: true,
			expected: `
Depends: ${misc:Depends},
         bar,
         foo`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			buf := &strings.Builder{}
			original := maps.Clone(test.runtimeDependencies)

			writeDepends(buf, test.runtimeDependencies, test.disableAutoRequires)

			assert.Check(t, cmp.Equal(strings.TrimSpace(buf.String()), strings.TrimSpace(test.expected)))
			assert.Assert(t, reflect.DeepEqual(test.runtimeDependencies, original), "runtime dependencies were mutated")
		})
	}
}

func TestRules_OverridePerms(t *testing.T) {
	tests := []struct {
		name      string
		artifacts dalec.Artifacts
		expected  string
	}{
		{
			name: "default permissions emit no override",
			artifacts: dalec.Artifacts{
				Binaries: map[string]dalec.ArtifactConfig{"example": {}},
			},
		},
		{
			name: "artifact permissions emit an override",
			artifacts: dalec.Artifacts{
				Binaries: map[string]dalec.ArtifactConfig{"example": {Permissions: 0o750}},
			},
			expected: "override_dh_fixperms:\n\tdh_fixperms\n\tdebian/dalec/fix_perms.sh\n\n",
		},
		{
			name: "config file permissions emit an override",
			artifacts: dalec.Artifacts{
				ConfigFiles: map[string]dalec.ArtifactConfig{"example": {Permissions: 0o750}},
			},
			expected: "override_dh_fixperms:\n\tdh_fixperms\n\tdebian/dalec/fix_perms.sh\n\n",
		},
		{
			name: "manpage permissions emit an override",
			artifacts: dalec.Artifacts{
				Manpages: map[string]dalec.ArtifactConfig{"example": {Permissions: 0o750}},
			},
			expected: "override_dh_fixperms:\n\tdh_fixperms\n\tdebian/dalec/fix_perms.sh\n\n",
		},
		{
			name: "header permissions emit an override",
			artifacts: dalec.Artifacts{
				Headers: map[string]dalec.ArtifactConfig{"example": {Permissions: 0o750}},
			},
			expected: "override_dh_fixperms:\n\tdh_fixperms\n\tdebian/dalec/fix_perms.sh\n\n",
		},
		{
			name: "license permissions emit an override",
			artifacts: dalec.Artifacts{
				Licenses: map[string]dalec.ArtifactConfig{"example": {Permissions: 0o750}},
			},
			expected: "override_dh_fixperms:\n\tdh_fixperms\n\tdebian/dalec/fix_perms.sh\n\n",
		},
		{
			name: "documentation permissions emit an override",
			artifacts: dalec.Artifacts{
				Docs: map[string]dalec.ArtifactConfig{"example": {Permissions: 0o750}},
			},
			expected: "override_dh_fixperms:\n\tdh_fixperms\n\tdebian/dalec/fix_perms.sh\n\n",
		},
		{
			name: "library permissions emit an override",
			artifacts: dalec.Artifacts{
				Libs: map[string]dalec.ArtifactConfig{"example": {Permissions: 0o750}},
			},
			expected: "override_dh_fixperms:\n\tdh_fixperms\n\tdebian/dalec/fix_perms.sh\n\n",
		},
		{
			name: "libexec permissions emit an override",
			artifacts: dalec.Artifacts{
				Libexec: map[string]dalec.ArtifactConfig{"example": {Permissions: 0o750}},
			},
			expected: "override_dh_fixperms:\n\tdh_fixperms\n\tdebian/dalec/fix_perms.sh\n\n",
		},
		{
			name: "data file permissions emit an override",
			artifacts: dalec.Artifacts{
				DataDirs: map[string]dalec.ArtifactConfig{"example": {Permissions: 0o750}},
			},
			expected: "override_dh_fixperms:\n\tdh_fixperms\n\tdebian/dalec/fix_perms.sh\n\n",
		},
		{
			name: "config directory permissions emit an override",
			artifacts: dalec.Artifacts{
				Directories: &dalec.CreateArtifactDirectories{
					Config: map[string]dalec.ArtifactDirConfig{"example": {Mode: 0o750}},
				},
			},
			expected: "override_dh_fixperms:\n\tdh_fixperms\n\tdebian/dalec/fix_perms.sh\n\n",
		},
		{
			name: "state directory permissions emit an override",
			artifacts: dalec.Artifacts{
				Directories: &dalec.CreateArtifactDirectories{
					State: map[string]dalec.ArtifactDirConfig{"example": {Mode: 0o700}},
				},
			},
			expected: "override_dh_fixperms:\n\tdh_fixperms\n\tdebian/dalec/fix_perms.sh\n\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			w := newRulesWrapper(test.artifacts)

			actual := w.OverridePerms().String()

			assert.Equal(t, actual, test.expected)
		})
	}
}

func TestRules_OverrideStrip(t *testing.T) {
	t.Run("strip enabled by default emits no overrides", func(t *testing.T) {
		w := newRulesWrapper(dalec.Artifacts{})
		assert.Equal(t, w.OverrideStrip().String(), "")
	})

	t.Run("disable_strip disables dh_strip and dh_strip_nondeterminism", func(t *testing.T) {
		w := newRulesWrapper(dalec.Artifacts{DisableStrip: true})
		assert.Equal(t, w.OverrideStrip().String(), "override_dh_strip:\noverride_dh_strip_nondeterminism:\n")
	})
}

func TestRules_OverrideAutoRequires(t *testing.T) {
	t.Run("auto-requires enabled by default lets dh_shlibdeps run", func(t *testing.T) {
		w := newRulesWrapper(dalec.Artifacts{})
		assert.Equal(t, w.OverrideAutoRequires().String(), "")
	})

	t.Run("disable_auto_requires disables dh_shlibdeps", func(t *testing.T) {
		w := newRulesWrapper(dalec.Artifacts{DisableAutoRequires: true})
		assert.Equal(t, w.OverrideAutoRequires().String(), "override_dh_shlibdeps:\n")
	})
}

func newRulesWrapper(artifacts dalec.Artifacts) *rulesWrapper {
	return &rulesWrapper{Spec: &dalec.Spec{Artifacts: artifacts}}
}
