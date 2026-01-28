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

		ctx := t.Context()

		ops, err := dalec.LLBOpsFromState(ctx, c.BuildContainer(ctx, &testClient{}, dalec.SourceOpts{}, &dalec.Spec{}, "target", llb.State{}))
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
			t.Errorf("Expected to find mount for extra install repo %q, but did not", extraInstallRepo)
		}
	})

	t.Run("installs_base_packages_before_installing_spec_package", func(t *testing.T) {
		t.Parallel()

		c := &distro.Config{
			DefaultOutputImage: "foo",
			BasePackages:       []string{"base-package-1"},
			VersionID:          "bar",
			ContextRef:         "distro-context-ref",
		}

		ctx := t.Context()

		sopt := dalec.SourceOpts{
			GetContext: func(string, ...llb.LocalOption) (*llb.State, error) {
				s := llb.Scratch()

				return &s, nil
			},
		}

		pkgSpec := &dalec.Spec{
			Name:     "spec-package",
			Packager: "foo",
		}

		pkg := c.BuildPkg(ctx, &testClient{}, sopt, pkgSpec, "target", dalec.ProgressGroup("foo"))

		state := c.BuildContainer(ctx, &testClient{}, sopt, &dalec.Spec{}, "target", pkg, dalec.ProgressGroup("foo"))

		ops, err := dalec.LLBOpsFromState(ctx, state)
		if err != nil {
			t.Fatalf("failed to get llb ops from state: %v", err)
		}

		basePkgFound := false
		pkgFound := false

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

				if string(mkfile.Path) != "/debian/control" {
					continue
				}

				if strings.Contains(string(mkfile.Data), "base-package-1") {
					if pkgFound {
						t.Errorf("Base package found after spec package")
					}

					basePkgFound = true
				}

				if strings.Contains(string(mkfile.Data), "spec-package") {
					pkgFound = true
				}
			}
		}

		if !basePkgFound {
			t.Errorf("Base package not found in installation")
		}

		s, err := dalec.LLBOpsToJSON(ops)
		if err != nil {
			t.Fatalf("failed to convert llb ops to json: %v", err)
		}

		_ = s
		// t.Errorf("LLB JSON: \n%s", s)
	})

	t.Run("mounts_apt_cache_before_installing_base_packages", func(t *testing.T) {
		t.Parallel()

		aptCachePrefix := "apt-cache-prefix"

		c := &distro.Config{
			DefaultOutputImage: "foo",
			BasePackages:       []string{"base-package-1"},
			VersionID:          "bar",
			ContextRef:         "distro-context-ref",
			AptCachePrefix:     aptCachePrefix,
		}

		ctx := t.Context()

		sopt := dalec.SourceOpts{
			GetContext: func(string, ...llb.LocalOption) (*llb.State, error) {
				s := llb.Scratch()

				return &s, nil
			},
		}

		state := c.BuildContainer(ctx, &testClient{}, sopt, &dalec.Spec{}, "target", llb.Scratch(), dalec.ProgressGroup("foo"))

		ops, err := dalec.LLBOpsFromState(ctx, state)
		if err != nil {
			t.Fatalf("failed to get llb ops from state: %v", err)
		}

		aptCacheFound := false

		for _, op := range ops {
			e := op.Op.GetExec()
			if e == nil || op.OpMetadata.ProgressGroup.Name != "Install base image packages" {
				continue
			}

			for _, mount := range e.Mounts {
				if mount.Dest == "/var/cache/apt" {
					aptCacheFound = true

					t.Run("with_configured_apt_cache_prefix", func(t *testing.T) {
						t.Parallel()

						if mount.CacheOpt == nil {
							t.Fatalf("Expected cache mount to have cache options, got none")
						}

						if !strings.HasPrefix(mount.CacheOpt.ID, aptCachePrefix) {
							t.Fatalf("Expected cache mount ID to have prefix %q, got %q", aptCachePrefix, mount.CacheOpt.ID)
						}
					})

					break
				}
			}
		}

		if !aptCacheFound {
			t.Fatalf("Apt cache mount not found before installing base packages")
		}

		return
		s, err := dalec.LLBOpsToJSON(ops)
		if err != nil {
			t.Fatalf("failed to convert llb ops to json: %v", err)
		}

		t.Errorf("LLB JSON: \n%s", s)
	})

	t.Run("mounts_apt_cache_after_installing_base_packages", func(t *testing.T) {
		t.Parallel()
	})
}

func Test_Build_package(t *testing.T) {
	t.Parallel()

	return

	c := &distro.Config{
		DefaultOutputImage: "foo",
		BasePackages:       []string{"base-package-1"},
		VersionID:          "bar",
	}

	ctx := t.Context()

	sopt := dalec.SourceOpts{
		GetContext: func(string, ...llb.LocalOption) (*llb.State, error) {
			s := llb.Scratch()

			return &s, nil
		},
	}

	state := c.BuildPkg(ctx, &testClient{}, sopt, &dalec.Spec{}, "target", dalec.ProgressGroup("foo"))

	ops, err := dalec.LLBOpsFromState(ctx, state)
	if err != nil {
		t.Fatalf("failed to get llb ops from state: %v", err)
	}

	s, err := dalec.LLBOpsToJSON(ops)
	if err != nil {
		t.Fatalf("failed to convert llb ops to json: %v", err)
	}

	t.Errorf("LLB JSON: %s", s)
}

func Test_Building_worker(t *testing.T) {
	t.Parallel()

	return

	c := &distro.Config{
		DefaultOutputImage: "distro-default-output-image",
		BasePackages:       []string{"base-package-1"},
		VersionID:          "distro-version-id",
		ContextRef:         "distro-context-ref",
	}

	ctx := t.Context()

	sopt := dalec.SourceOpts{
		GetContext: func(string, ...llb.LocalOption) (*llb.State, error) {
			s := llb.Scratch()

			return &s, nil
		},
	}

	state := c.Worker(sopt)

	ops, err := dalec.LLBOpsFromState(ctx, state)
	if err != nil {
		t.Fatalf("failed to get llb ops from state: %v", err)
	}

	s, err := dalec.LLBOpsToJSON(ops)
	if err != nil {
		t.Fatalf("failed to convert llb ops to json: %v", err)
	}

	t.Errorf("LLB JSON: %s", s)
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
