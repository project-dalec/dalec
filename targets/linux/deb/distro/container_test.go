package distro

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
	"github.com/project-dalec/dalec/internal/test"
)

func Test_Building_container(t *testing.T) {
	t.Parallel()

	// Base image is test suite specific, since RPM packages can not always be installed on base
	// DEB image, hence local test instead of integration one.
	t.Run("uses_base_image_from_spec", func(t *testing.T) {
		t.Parallel()

		c := &Config{
			ImageRef:           "foo",
			DefaultOutputImage: "foo",
		}

		client := &testClient{
			buildOpts: client.BuildOpts{},
		}

		expectedRef := "bar"

		spec := &dalec.Spec{
			Image: &dalec.ImageConfig{
				Bases: []dalec.BaseImage{
					{
						Rootfs: dalec.Source{
							DockerImage: &dalec.SourceDockerImage{
								Ref: expectedRef,
							},
						},
					},
				},
			},
		}

		ctx := t.Context()

		sopt := dalec.SourceOpts{
			GetContext: func(string, ...llb.LocalOption) (*llb.State, error) {
				return nil, nil
			},
		}

		state := c.BuildContainer(ctx, client, sopt, spec, "target", llb.State{})

		ops, err := test.LLBOpsFromState(ctx, state)
		if err != nil {
			t.Fatalf("failed to get llb ops from state: %v", err)
		}

		specPackageImageSourceFound := false

		for _, op := range ops {
			s := op.Op.GetSource()

			if s == nil || op.OpMetadata.ProgressGroup.Name != "Build Container Image" {
				continue
			}

			specPackageImageSourceFound = true

			if !strings.Contains(s.Identifier, expectedRef) {
				t.Fatalf("expected source identifier to contain %q, got %q", expectedRef, s.Identifier)
			}
		}

		if !specPackageImageSourceFound {
			t.Fatalf("Expected to find spec package source in llb ops")
		}
	})

	// Most tests here must assert values in Config which are not user-configurable, hence
	// they cannot be placed in integration tests.
	t.Run("uses_default_output_image_if_spec_does_not_set_one", func(t *testing.T) {
		t.Parallel()

		expectedRef := "foo"

		c := &Config{
			ImageRef:           "foo",
			DefaultOutputImage: expectedRef,
		}

		client := &testClient{
			buildOpts: client.BuildOpts{},
		}

		spec := &dalec.Spec{}

		ctx := t.Context()

		sopt := dalec.SourceOpts{
			GetContext: func(string, ...llb.LocalOption) (*llb.State, error) {
				return nil, nil
			},
		}

		state := c.BuildContainer(ctx, client, sopt, spec, "target", llb.State{})

		ops, err := test.LLBOpsFromState(ctx, state)
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

		if !strings.Contains(s.Identifier, expectedRef) {
			t.Fatalf("expected source identifier to contain %q, got %q", expectedRef, s.Identifier)
		}
	})

	t.Run("when_bootstrapping_an_image", func(t *testing.T) {
		t.Parallel()

		// Bootstrap path is taken when DefaultOutputImage is not set.
		// This is not user-configurable, hence it cannot be tested via integration tests.
		t.Run("creates_base_directory_structure", func(t *testing.T) {
			t.Parallel()

			c := &Config{
				ImageRef: "foo",
				// DefaultOutputImage is intentionally empty to trigger bootstrap path.
			}

			ctx := t.Context()

			sopt := dalec.SourceOpts{
				GetContext: func(string, ...llb.LocalOption) (*llb.State, error) {
					return nil, nil
				},
			}

			ops, err := test.LLBOpsFromState(ctx, c.BuildContainer(ctx, &testClient{}, sopt, &dalec.Spec{}, "target", llb.Scratch()))
			if err != nil {
				t.Fatalf("failed to get llb ops from state: %v", err)
			}

			for _, op := range ops {
				if op.OpMetadata.ProgressGroup != nil && op.OpMetadata.ProgressGroup.Name == "Bootstrap Base Image" {
					return
				}
			}

			t.Fatalf("Expected bootstrap directory structure when DefaultOutputImage is not set")
		})

		t.Run("downloads_dependencies", func(t *testing.T) {
			t.Parallel()

			t.Run("with_extra_distro_config_repos_mounted", func(t *testing.T) {
				t.Parallel()

				extraInstallRepo := "extra-install-repo"

				c := &Config{
					ImageRef: "foo",
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

				sopt := dalec.SourceOpts{
					GetContext: func(string, ...llb.LocalOption) (*llb.State, error) {
						return nil, nil
					},
				}

				ops, err := test.LLBOpsFromState(ctx, c.BuildContainer(ctx, &testClient{}, sopt, &dalec.Spec{}, "target", llb.Scratch()))
				if err != nil {
					t.Fatalf("failed to get llb ops from state: %v", err)
				}

				expectedMountPath := "/etc/apt/sources.list.d/" + extraInstallRepo + ".list"

				for _, op := range ops {
					e := op.Op.GetExec()
					if e == nil {
						continue
					}

					// Find the bootstrap download exec by its unique install script mount.
					isBootstrapExec := false
					for _, mount := range e.Mounts {
						if mount.Dest == "/tmp/install.sh" {
							isBootstrapExec = true
							break
						}
					}

					if !isBootstrapExec {
						continue
					}

					for _, mount := range e.Mounts {
						if mount.Dest == expectedMountPath {
							return
						}
					}

					t.Fatalf("Bootstrap download exec does not have extra repo mount at %q", expectedMountPath)
				}

				t.Fatalf("No bootstrap download exec found")
			})

			t.Run("with_mounted_apt_cache", func(t *testing.T) {
				t.Parallel()

				aptCachePrefix := "apt-cache-prefix"

				c := &Config{
					ImageRef:       "foo",
					AptCachePrefix: aptCachePrefix,
				}

				ctx := t.Context()

				sopt := dalec.SourceOpts{
					GetContext: func(string, ...llb.LocalOption) (*llb.State, error) {
						return nil, nil
					},
				}

				ops, err := test.LLBOpsFromState(ctx, c.BuildContainer(ctx, &testClient{}, sopt, &dalec.Spec{}, "target", llb.Scratch()))
				if err != nil {
					t.Fatalf("failed to get llb ops from state: %v", err)
				}

				for _, op := range ops {
					e := op.Op.GetExec()
					if e == nil {
						continue
					}

					// Find the bootstrap download exec by its unique install script mount.
					isBootstrapExec := false
					for _, mount := range e.Mounts {
						if mount.Dest == "/tmp/install.sh" {
							isBootstrapExec = true
							break
						}
					}

					if !isBootstrapExec {
						continue
					}

					for _, mount := range e.Mounts {
						if mount.Dest == "/var/cache/apt" {
							if mount.CacheOpt == nil {
								t.Fatalf("Expected cache mount to have cache options, got none")
							}

							if !strings.HasPrefix(mount.CacheOpt.ID, aptCachePrefix) {
								t.Fatalf("Expected cache mount ID to have prefix %q, got %q", aptCachePrefix, mount.CacheOpt.ID)
							}

							return
						}
					}

					t.Fatalf("Apt cache mount not found on bootstrap download exec")
				}

				t.Fatalf("No bootstrap download exec found")
			})
		})
	})

	t.Run("installs_spec_package", func(t *testing.T) {
		t.Parallel()

		t.Run("with_extra_distro_config_repos_mounted", func(t *testing.T) {
			t.Parallel()

			extraInstallRepo := "extra-install-repo"

			c := &Config{
				ImageRef:           "foo",
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

			sopt := dalec.SourceOpts{
				GetContext: func(string, ...llb.LocalOption) (*llb.State, error) {
					return nil, nil
				},
			}
			ops, err := test.LLBOpsFromState(ctx, c.BuildContainer(ctx, &testClient{}, sopt, &dalec.Spec{}, "target", llb.State{}))
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

		t.Run("with_mounted_apt_cache", func(t *testing.T) {
			t.Parallel()

			aptCachePrefix := "apt-cache-prefix"

			c := &Config{
				ImageRef:           "foo",
				DefaultOutputImage: "foo",
				VersionID:          "bar",
				ContextRef:         "distro-context-ref",
				AptCachePrefix:     aptCachePrefix,
			}

			ctx := t.Context()

			sopt := dalec.SourceOpts{
				GetContext: func(string, ...llb.LocalOption) (*llb.State, error) {
					return nil, nil
				},
			}

			state := c.BuildContainer(ctx, &testClient{}, sopt, &dalec.Spec{}, "target", llb.Scratch(), dalec.ProgressGroup("foo"))

			ops, err := test.LLBOpsFromState(ctx, state)
			if err != nil {
				t.Fatalf("failed to get llb ops from state: %v", err)
			}

			aptCacheFound := false

			for _, op := range ops {
				e := op.Op.GetExec()
				if e == nil {
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
		})
	})

	t.Run("installs_base_packages", func(t *testing.T) {
		t.Parallel()

		t.Run("with_upgrades_enabled", func(t *testing.T) {
			t.Parallel()

			c := &Config{
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

			state := c.BuildContainer(ctx, &testClient{}, sopt, &dalec.Spec{}, "target", llb.Scratch(), dalec.ProgressGroup("foo"))

			ops, err := test.LLBOpsFromState(ctx, state)
			if err != nil {
				t.Fatalf("failed to get llb ops from state: %v", err)
			}

			for _, op := range ops {
				e := op.Op.GetExec()
				if e == nil || op.OpMetadata.ProgressGroup.Name != "Install base image packages" {
					continue
				}

				for _, v := range e.Meta.Env {
					s := strings.Split(v, "=")
					if len(s) != 2 {
						continue
					}

					if s[0] != "DALEC_UPGRADE" {
						continue
					}

					expectedValue := "true"

					if s[1] != expectedValue {
						t.Fatalf("Expected DALEC_UPGRADE env to be %q, got %q", expectedValue, s[1])
					}

					return
				}
			}

			t.Fatalf("Expected DALEC_UPGRADE to be set when installing base packages")
		})

		t.Run("before_installing_spec_package", func(t *testing.T) {
			t.Parallel()

			c := &Config{
				ImageRef:           "foo",
				DefaultOutputImage: "foo",
				BasePackages:       []string{"base-package-1"},
				VersionID:          "bar",
				ContextRef:         "distro-context-ref",
			}

			ctx := t.Context()

			sopt := dalec.SourceOpts{
				GetContext: func(string, ...llb.LocalOption) (*llb.State, error) {
					return nil, nil
				},
			}

			pkgSpec := &dalec.Spec{
				Name:     "spec-package",
				Packager: "foo",
			}

			pkg := c.BuildPkg(ctx, &testClient{}, sopt, pkgSpec, "target", dalec.ProgressGroup("foo"))

			state := c.BuildContainer(ctx, &testClient{}, sopt, &dalec.Spec{}, "target", pkg, dalec.ProgressGroup("foo"))

			ops, err := test.LLBOpsFromState(ctx, state)
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
		})

		t.Run("with_apt_cache_mounts", func(t *testing.T) {
			t.Parallel()

			aptCachePrefix := "apt-cache-prefix"

			c := &Config{
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

			ops, err := test.LLBOpsFromState(ctx, state)
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
		})
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
