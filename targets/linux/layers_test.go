package linux

import (
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/internal/layersplit"
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

func TestPreviousImageAppPackages(t *testing.T) {
	img := &dockerspec.DockerOCIImage{}
	img.Config.Labels = map[string]string{layersplit.LabelSpecPackages: "foo,foo-meta"}
	img.History = []ocispecs.History{
		{Comment: layersplit.Group{Name: "glibc", Kind: layersplit.KindOS, Packages: []string{"glibc"}}.HistoryComment()},
		{Comment: layersplit.Group{Name: "foo-old", Kind: layersplit.KindApp, Packages: []string{"foo-old"}}.HistoryComment()},
		{Comment: "buildkit.exporter.image.v0"},
	}
	prev := &previousImage{Config: img}
	assert.Check(t, cmp.DeepEqual(prev.appPackages(), []string{"foo", "foo-meta", "foo-old"}))
	assert.Check(t, cmp.Len((&previousImage{}).appPackages(), 0))
}
