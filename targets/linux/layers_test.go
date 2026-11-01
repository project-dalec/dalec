package linux

import (
	"testing"

	"github.com/project-dalec/dalec"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestSpecPackageNames(t *testing.T) {
	spec := &dalec.Spec{
		Name: "foo",
		Targets: map[string]dalec.Target{
			"azlinux3": {
				Packages: map[string]dalec.SubPackage{
					"debug":  {Description: "debug"},
					"compat": {Name: "foo-compat-v2", Description: "compat"},
				},
			},
		},
	}

	got := specPackageNames(spec, "azlinux3", []string{"foo", "legacy-name"})
	assert.Check(t, cmp.DeepEqual(got, []string{"foo", "foo-compat-v2", "foo-debug", "legacy-name"}))
	assert.Check(t, cmp.DeepEqual(specPackageNames(spec, "jammy", nil), []string{"foo"}))
}

func TestParseUpdatePackages(t *testing.T) {
	got, err := parseUpdatePackages("openssl, openssl-libs python3.12\tglibc* lib?z c++-libs perl:Error")
	assert.NilError(t, err)
	assert.Check(t, cmp.DeepEqual(got, []string{"openssl", "openssl-libs", "python3.12", "glibc*", "lib?z", "c++-libs", "perl:Error"}))

	got, err = parseUpdatePackages("")
	assert.NilError(t, err)
	assert.Check(t, cmp.Len(got, 0))

	for _, bad := range []string{"-y", "--setopt=reposdir=/tmp", "$(id)", "a;b", "`id`", "a|b", "pkg/../x"} {
		_, err := parseUpdatePackages(bad)
		assert.Check(t, cmp.ErrorContains(err, "invalid package name"), bad)
	}
}
