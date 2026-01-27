package distro_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	"github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/targets/linux/deb/distro"
)

func Test_Building_container(t *testing.T) {
	t.Parallel()

	t.Run("uses_default_output_image_if_spec_does_not_set_one", func(t *testing.T) {
		t.Parallel()

		c := &distro.Config{
			DefaultOutputImage: "foo",
		}

		client := &testClient{
			buildOpts: client.BuildOpts{},
		}

		spec := &dalec.Spec{}

		state := c.BuildContainer(context.TODO(), client, dalec.SourceOpts{}, spec, "target", llb.State{})

		ops, err := dalec.LLBOpsFromState(context.TODO(), state)
		if err != nil {
			t.Fatalf("failed to get llb ops from state: %v", err)
		}

		if len(ops) == 0 {
			t.Fatalf("expected at least one llb op, got none")
		}

		s := ops[0].Op.GetSource()
		if s == nil {
			t.Fatalf("expected source op, got nil")
		}

		if !strings.Contains(s.Identifier, "foo") {
			t.Fatalf("expected source identifier to contain 'foo', got %q", s.Identifier)
		}
	})

	t.Run("mounts_extra_repos_for_installation", func(t *testing.T) {
		t.Parallel()

		extraInstallRepo := "extra-install-repo"

		c := &distro.Config{
			DefaultOutputImage: "foo",
			ExtraRepos: []dalec.PackageRepositoryConfig{
				{
					Envs: []string{"install"},
					Config: map[string]dalec.Source{
						extraInstallRepo: {
							Inline: &dalec.SourceInline{
								File: &dalec.SourceInlineFile{
									Contents: extraInstallRepo,
								},
							},
						},
					},
				},
				{
					Envs: []string{"build"},
					Config: map[string]dalec.Source{
						extraInstallRepo: {
							Inline: &dalec.SourceInline{
								File: &dalec.SourceInlineFile{
									Contents: "unexpected-repo",
								},
							},
						},
					},
				},
			},
		}

		client := &testClient{
			buildOpts: client.BuildOpts{},
		}

		spec := &dalec.Spec{}

		state := c.BuildContainer(context.TODO(), client, dalec.SourceOpts{}, spec, "target", llb.State{})

		ops, err := dalec.LLBOpsFromState(context.TODO(), state)
		if err != nil {
			t.Fatalf("failed to get llb ops from state: %v", err)
		}

		found := false
		for _, op := range ops {
			f := op.Op.GetFile()
			if f == nil {
				continue
			}

			for _, a := range f.Actions {
				mkfile := a.GetMkfile()
				if mkfile == nil {
					continue
				}

				t.Error("at configured prefix")
				if string(mkfile.Data) == extraInstallRepo {
					found = true
					break
				}

				if string(mkfile.Data) == "unexpected-repo" {
					t.Errorf("Found mount for extra install repo in wrong env")
				}
			}
		}

		if !found {
			t.Fatalf("Expected to find mount for extra install repo %q, but did not", extraInstallRepo)
		}
	})
}

// testClient should implement client.Client interface for testing purposes.
type testClient struct {
	buildOpts client.BuildOpts
}

func (tc *testClient) BuildOpts() client.BuildOpts {
	return tc.buildOpts
}

func (tc *testClient) Solve(ctx context.Context, req client.SolveRequest) (*client.Result, error) {
	return nil, nil
}

func (tc *testClient) Inputs(ctx context.Context) (map[string]llb.State, error) {
	return nil, errors.New("not implemented")
}

func (tc *testClient) NewContainer(ctx context.Context, req client.NewContainerRequest) (client.Container, error) {
	return nil, errors.New("not implemented")
}

func (tc *testClient) ResolveImageConfig(ctx context.Context, ref string, opt sourceresolver.Opt) (string, digest.Digest, []byte, error) {
	return "", "", nil, errors.New("not implemented")
}

func (tc *testClient) ResolveSourceMetadata(ctx context.Context, op *pb.SourceOp, opt sourceresolver.Opt) (*sourceresolver.MetaResponse, error) {
	return nil, errors.New("not implemented")
}

func (tc *testClient) Warn(ctx context.Context, dgst digest.Digest, msg string, opts client.WarnOpts) error {
	return errors.New("not implemented")
}
