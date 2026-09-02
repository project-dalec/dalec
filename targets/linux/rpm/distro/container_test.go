package distro

import (
	"context"
	"errors"
	"testing"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	"github.com/project-dalec/dalec"
	"gotest.tools/v3/assert"
)

func TestBuildContainerBasePackageInstallOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		separate     bool
		wantInstalls [][]string
	}{
		{
			name:     "combined",
			separate: false,
			wantInstalls: [][]string{{
				"/tmp/rpms/**/*.rpm",
				"/tmp/rpms-base/**/*.rpm",
			}},
		},
		{
			name:     "separate",
			separate: true,
			wantInstalls: [][]string{
				{"/tmp/rpms-base/**/*.rpm"},
				{"/tmp/rpms/**/*.rpm"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var installs [][]string
			cfg := &Config{
				ContextRef: "worker",
				BasePackages: []dalec.Spec{{
					Name:        "dalec-base-test",
					Version:     "1.0.0",
					Revision:    "1",
					License:     "MIT",
					Description: "Test base package",
				}},
				InstallBasePackagesSeparately: tc.separate,
				InstallFunc: func(_ *dnfInstallConfig, _ string, pkgs []string) llb.RunOption {
					installs = append(installs, append([]string(nil), pkgs...))
					return llb.Args(append([]string{"install"}, pkgs...))
				},
			}
			sOpt := dalec.SourceOpts{
				GetContext: func(string, ...llb.LocalOption) (*llb.State, error) {
					st := llb.Scratch()
					return &st, nil
				},
			}

			cfg.BuildContainer(
				context.Background(),
				&containerTestClient{},
				sOpt,
				&dalec.Spec{},
				"test",
				llb.Scratch(),
			)

			assert.DeepEqual(t, installs, tc.wantInstalls)
		})
	}
}

type containerTestClient struct{}

func (c *containerTestClient) BuildOpts() gwclient.BuildOpts {
	return gwclient.BuildOpts{}
}

func (c *containerTestClient) Solve(context.Context, gwclient.SolveRequest) (*gwclient.Result, error) {
	return nil, errors.New("not implemented")
}

func (c *containerTestClient) Inputs(context.Context) (map[string]llb.State, error) {
	return nil, errors.New("not implemented")
}

func (c *containerTestClient) NewContainer(context.Context, gwclient.NewContainerRequest) (gwclient.Container, error) {
	return nil, errors.New("not implemented")
}

func (c *containerTestClient) ResolveImageConfig(context.Context, string, sourceresolver.Opt) (string, digest.Digest, []byte, error) {
	return "", "", nil, errors.New("not implemented")
}

func (c *containerTestClient) ResolveSourceMetadata(context.Context, *pb.SourceOp, sourceresolver.Opt) (*sourceresolver.MetaResponse, error) {
	return nil, errors.New("not implemented")
}

func (c *containerTestClient) Warn(context.Context, digest.Digest, string, gwclient.WarnOpts) error {
	return errors.New("not implemented")
}
