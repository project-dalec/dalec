package deb

import (
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
}

func TestDepends(t *testing.T) {
	control := &controlWrapper{
		Spec: &dalec.Spec{},
	}

	buf := &strings.Builder{}
	control.depends(buf, nil)

	expect := `
Depends: ${misc:Depends},
         ${shlibs:Depends}
`
	actual := strings.TrimSpace(buf.String())
	assert.Check(t, cmp.Equal(actual, strings.TrimSpace(expect)))

	buf.Reset()

	// Test again with non-nil deps
	control.depends(buf, &dalec.PackageDependencies{})
	actual = strings.TrimSpace(buf.String())
	assert.Check(t, cmp.Equal(actual, strings.TrimSpace(expect)))

	buf.Reset()

	// Test again with non-nil runtime deps
	control.depends(buf, &dalec.PackageDependencies{
		Runtime: map[string]dalec.PackageConstraints{},
	})
	actual = strings.TrimSpace(buf.String())
	assert.Check(t, cmp.Equal(actual, strings.TrimSpace(expect)))

	buf.Reset()

	// Test again with other runtime deps
	control.depends(buf, &dalec.PackageDependencies{
		Runtime: map[string]dalec.PackageConstraints{
			"foo": {},
			"bar": {},
		},
	})

	expect = `
Depends: ${misc:Depends},
         ${shlibs:Depends},
         bar,
         foo
`
	actual = strings.TrimSpace(buf.String())
	assert.Check(t, cmp.Equal(actual, strings.TrimSpace(expect)))

	buf.Reset()

	// Test again with other runtime deps and shlibs specified
	control.depends(buf, &dalec.PackageDependencies{
		Runtime: map[string]dalec.PackageConstraints{
			"foo":               {},
			"bar":               {},
			"${shlibs:Depends}": {},
		},
	})

	actual = strings.TrimSpace(buf.String())
	assert.Check(t, cmp.Equal(actual, strings.TrimSpace(expect)))

	buf.Reset()

	// Test again with other runtime deps and shlibs and misc depends specified
	control.depends(buf, &dalec.PackageDependencies{
		Runtime: map[string]dalec.PackageConstraints{
			"foo":               {},
			"bar":               {},
			"${shlibs:Depends}": {},
			"${misc:Depends}":   {},
		},
	})

	actual = strings.TrimSpace(buf.String())
	assert.Check(t, cmp.Equal(actual, strings.TrimSpace(expect)))
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
