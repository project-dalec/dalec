package deb

import (
	"strings"
	"testing"

	"github.com/project-dalec/dalec"
	"gotest.tools/v3/assert"
)

const (
	autoRequiresEnabledDepends  = "Depends: ${misc:Depends},\n         ${shlibs:Depends}"
	autoRequiresDisabledDepends = "Depends: ${misc:Depends}"
)

func TestDisableAutoRequiresIsPackageLocal(t *testing.T) {
	t.Run("Given only the root package disables auto-requires, when Debian metadata is generated, then supplemental packages remain enabled", func(t *testing.T) {
		spec := autoRequiresSpec(true, map[string]dalec.SubPackage{
			"libs":  autoRequiresSubPackage(&dalec.Artifacts{}),
			"tools": autoRequiresSubPackage(&dalec.Artifacts{}),
		})

		assertAutoRequiresMetadata(t, spec, map[string]string{
			"test-pkg":       autoRequiresDisabledDepends,
			"test-pkg-libs":  autoRequiresEnabledDepends,
			"test-pkg-tools": autoRequiresEnabledDepends,
		}, "override_dh_shlibdeps:\n\tdh_shlibdeps -Ntest-pkg\n")
	})

	t.Run("Given one supplemental package disables auto-requires, when Debian metadata is generated, then the root and its sibling remain enabled", func(t *testing.T) {
		spec := autoRequiresSpec(false, map[string]dalec.SubPackage{
			"libs":  autoRequiresSubPackage(&dalec.Artifacts{DisableAutoRequires: true}),
			"tools": autoRequiresSubPackage(&dalec.Artifacts{}),
		})

		assertAutoRequiresMetadata(t, spec, map[string]string{
			"test-pkg":       autoRequiresEnabledDepends,
			"test-pkg-libs":  autoRequiresDisabledDepends,
			"test-pkg-tools": autoRequiresEnabledDepends,
		}, "override_dh_shlibdeps:\n\tdh_shlibdeps -Ntest-pkg-libs\n")
	})

	t.Run("Given the root and a subset of supplemental packages disable auto-requires, when Debian metadata is generated, then enabled siblings retain discovery", func(t *testing.T) {
		spec := autoRequiresSpec(true, map[string]dalec.SubPackage{
			"libs":  autoRequiresSubPackage(&dalec.Artifacts{DisableAutoRequires: true}),
			"tools": autoRequiresSubPackage(&dalec.Artifacts{}),
		})

		assertAutoRequiresMetadata(t, spec, map[string]string{
			"test-pkg":       autoRequiresDisabledDepends,
			"test-pkg-libs":  autoRequiresDisabledDepends,
			"test-pkg-tools": autoRequiresEnabledDepends,
		}, "override_dh_shlibdeps:\n\tdh_shlibdeps -Ntest-pkg -Ntest-pkg-libs\n")
	})

	t.Run("Given multiple supplemental packages disable auto-requires, when Debian rules are generated, then exclusions are sorted by resolved name", func(t *testing.T) {
		spec := autoRequiresSpec(false, map[string]dalec.SubPackage{
			"zeta":  autoRequiresSubPackage(&dalec.Artifacts{DisableAutoRequires: true}),
			"alpha": autoRequiresSubPackage(&dalec.Artifacts{DisableAutoRequires: true}),
			"tools": autoRequiresSubPackage(&dalec.Artifacts{}),
		})

		assertAutoRequiresMetadata(t, spec, map[string]string{
			"test-pkg":       autoRequiresEnabledDepends,
			"test-pkg-alpha": autoRequiresDisabledDepends,
			"test-pkg-tools": autoRequiresEnabledDepends,
			"test-pkg-zeta":  autoRequiresDisabledDepends,
		}, "override_dh_shlibdeps:\n\tdh_shlibdeps -Ntest-pkg-alpha -Ntest-pkg-zeta\n")
	})

	t.Run("Given every binary package disables auto-requires, when Debian rules are generated, then dh_shlibdeps is not invoked", func(t *testing.T) {
		spec := autoRequiresSpec(true, map[string]dalec.SubPackage{
			"libs":  autoRequiresSubPackage(&dalec.Artifacts{DisableAutoRequires: true}),
			"tools": autoRequiresSubPackage(&dalec.Artifacts{DisableAutoRequires: true}),
		})

		assertAutoRequiresMetadata(t, spec, map[string]string{
			"test-pkg":       autoRequiresDisabledDepends,
			"test-pkg-libs":  autoRequiresDisabledDepends,
			"test-pkg-tools": autoRequiresDisabledDepends,
		}, "override_dh_shlibdeps:\n")
	})

	t.Run("Given a disabled supplemental package has a custom name, when Debian metadata is generated, then control and rules use the same resolved name", func(t *testing.T) {
		custom := autoRequiresSubPackage(&dalec.Artifacts{DisableAutoRequires: true})
		custom.Name = "custom-runtime"
		spec := autoRequiresSpec(false, map[string]dalec.SubPackage{
			"libs": custom,
		})

		assertAutoRequiresMetadata(t, spec, map[string]string{
			"test-pkg":       autoRequiresEnabledDepends,
			"custom-runtime": autoRequiresDisabledDepends,
		}, "override_dh_shlibdeps:\n\tdh_shlibdeps -Ncustom-runtime\n")
	})

	t.Run("Given supplemental artifacts are nil or unset, when Debian metadata is generated, then automatic discovery remains enabled", func(t *testing.T) {
		spec := autoRequiresSpec(false, map[string]dalec.SubPackage{
			"nil":   autoRequiresSubPackage(nil),
			"unset": autoRequiresSubPackage(&dalec.Artifacts{}),
		})

		assertAutoRequiresMetadata(t, spec, map[string]string{
			"test-pkg":       autoRequiresEnabledDepends,
			"test-pkg-nil":   autoRequiresEnabledDepends,
			"test-pkg-unset": autoRequiresEnabledDepends,
		}, "")
	})
}

func assertAutoRequiresMetadata(t *testing.T, spec *dalec.Spec, expectedDepends map[string]string, expectedRules string) {
	t.Helper()

	actualDepends := generatedPackageDepends(t, spec)
	assert.DeepEqual(t, actualDepends, expectedDepends)

	rules := (&rulesWrapper{Spec: spec, target: "test"}).OverrideAutoRequires().String()
	assert.Equal(t, rules, expectedRules)
}

func generatedPackageDepends(t *testing.T, spec *dalec.Spec) map[string]string {
	t.Helper()

	var control strings.Builder
	assert.NilError(t, WriteControl(spec, "test", &control))

	depends := make(map[string]string)
	for _, paragraph := range strings.Split(strings.TrimSpace(control.String()), "\n\n") {
		lines := strings.Split(paragraph, "\n")
		name := controlPackageName(lines)
		if name == "" {
			continue
		}
		depends[name] = controlDepends(lines)
	}
	return depends
}

func controlPackageName(lines []string) string {
	for _, line := range lines {
		if strings.HasPrefix(line, "Package:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Package:"))
		}
	}
	return ""
}

func controlDepends(lines []string) string {
	for i, line := range lines {
		if !strings.HasPrefix(line, "Depends:") {
			continue
		}

		depends := []string{line}
		for _, continuation := range lines[i+1:] {
			if !strings.HasPrefix(continuation, " ") {
				break
			}
			depends = append(depends, continuation)
		}
		return strings.Join(depends, "\n")
	}
	return ""
}

func autoRequiresSpec(rootDisabled bool, packages map[string]dalec.SubPackage) *dalec.Spec {
	return &dalec.Spec{
		Name:        "test-pkg",
		Version:     "1.0.0",
		Description: "Primary package",
		Targets: map[string]dalec.Target{
			"test": {
				Artifacts: &dalec.Artifacts{DisableAutoRequires: rootDisabled},
				Packages:  packages,
			},
		},
	}
}

func autoRequiresSubPackage(artifacts *dalec.Artifacts) dalec.SubPackage {
	return dalec.SubPackage{
		Description: "Supplemental package",
		Artifacts:   artifacts,
	}
}
