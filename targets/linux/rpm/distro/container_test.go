package distro

import (
	"path/filepath"
	"testing"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/project-dalec/dalec"
	"gotest.tools/v3/assert"
)

func TestBuildContainerInstallsAllGeneratedBinaryRPMs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		archDir string
		rpm     string
	}{
		{
			name:    "the primary package is selected",
			archDir: "x86_64",
			rpm:     "myapp-1.0.0-1.azl3.x86_64.rpm",
		},
		{
			name:    "a default supplemental package is selected",
			archDir: "x86_64",
			rpm:     "myapp-contrib-1.0.0-1.azl3.x86_64.rpm",
		},
		{
			name:    "a custom-named supplemental package is selected",
			archDir: "x86_64",
			rpm:     "my-custom-pkg-1.0.0-1.azl3.x86_64.rpm",
		},
		{
			name:    "a documentation package is selected",
			archDir: "noarch",
			rpm:     "myapp-docs-1.0.0-1.azl3.noarch.rpm",
		},
		{
			name:    "a debug package is selected",
			archDir: "x86_64",
			rpm:     "myapp-debuginfo-1.0.0-1.azl3.x86_64.rpm",
		},
		{
			name:    "a compatibility package is selected",
			archDir: "aarch64",
			rpm:     "myapp-compat-1.0.0-1.azl3.aarch64.rpm",
		},
	}

	var installed []string
	cfg := &Config{
		ContextRef: "worker",
		InstallFunc: func(_ *dnfInstallConfig, _ string, pkgs []string) llb.RunOption {
			installed = append(installed, pkgs...)
			return llb.Args([]string{"true"})
		},
	}
	sOpt := dalec.SourceOpts{
		GetContext: func(string, ...llb.LocalOption) (*llb.State, error) {
			st := llb.Scratch()
			return &st, nil
		},
	}

	cfg.BuildContainer(t.Context(), &containerTestClient{}, sOpt, &dalec.Spec{}, "target", llb.Scratch())

	assert.Assert(t, len(installed) == 1)
	assert.Equal(t, installed[0], filepath.Join("/tmp/rpms", "*/*.rpm"))

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			matches, err := filepath.Match(installed[0], filepath.Join("/tmp/rpms", tc.archDir, tc.rpm))
			assert.NilError(t, err)
			assert.Assert(t, matches, "pattern %q should select RPM %q", installed[0], tc.rpm)
		})
	}
}

type containerTestClient struct {
	gwclient.Client // Unexpected gateway calls fail through the nil embedded client.
}

func (*containerTestClient) BuildOpts() gwclient.BuildOpts {
	return gwclient.BuildOpts{}
}
