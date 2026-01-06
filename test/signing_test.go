package test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/test/testenv"
	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/stretchr/testify/assert"
)

func runTest(t *testing.T, f testenv.TestFunc, opts ...testenv.TestRunnerOpt) {
	t.Helper()
	ctx := startTestSpan(baseCtx, t)
	testEnv.RunTest(ctx, t, f, opts...)
}

func newSimpleSpec() *dalec.Spec {
	spec := fillMetadata("foo", &dalec.Spec{
		Sources: map[string]dalec.Source{
			"foo": {
				Inline: &dalec.SourceInline{
					File: &dalec.SourceInlineFile{
						Contents: "#!/usr/bin/env bash\necho \"hello, world!\"\n",
					},
				},
			},
		},
		Build: dalec.ArtifactBuild{
			Steps: []dalec.BuildStep{
				{
					Command: "/bin/true",
				},
			},
		},
		Artifacts: dalec.Artifacts{
			Binaries: map[string]dalec.ArtifactConfig{
				"foo": {},
			},
		},
	})

	return spec
}

func linuxSigningTests(ctx context.Context, testConfig testLinuxConfig) func(*testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		newSigningSpec := func() *dalec.Spec {
			spec := newSimpleSpec()
			spec.PackageConfig = &dalec.PackageConfig{
				Signer: &dalec.PackageSigner{
					Frontend: &dalec.Frontend{
						Image: phonySignerRef,
					},
				},
			}

			return spec
		}

		t.Run("root config", func(t *testing.T) {
			t.Parallel()
			spec := newSigningSpec()
			runTest(t, distroSigningTest(t, spec, testConfig.Target.Package, testConfig))
		})

		t.Run("with target config", func(t *testing.T) {
			t.Parallel()
			spec := newSigningSpec()
			first, _, _ := strings.Cut(testConfig.Target.Package, "/")
			spec.Targets = map[string]dalec.Target{
				first: {
					PackageConfig: &dalec.PackageConfig{
						Signer: spec.PackageConfig.Signer,
					},
				},
			}
			spec.PackageConfig.Signer = nil

			runTest(t, distroSigningTest(t, spec, testConfig.Target.Package, testConfig))
		})

		t.Run("target config takes precedence when root config is there", func(t *testing.T) {
			t.Parallel()
			spec := newSigningSpec()

			var found bool
			handleStatus := func(status *client.SolveStatus) {
				if found || status == nil {
					return
				}
				for _, w := range status.Warnings {
					if strings.Contains(string(w.Short), "Root signing spec overridden") {
						found = true
						return
					}
				}
			}

			first, _, _ := strings.Cut(testConfig.Target.Package, "/")
			spec.Targets = map[string]dalec.Target{
				first: {
					PackageConfig: &dalec.PackageConfig{
						Signer: &dalec.PackageSigner{
							Frontend: &dalec.Frontend{
								Image: spec.PackageConfig.Signer.Image,
							},
						},
					},
				},
			}

			spec.PackageConfig.Signer.Image = "notexist"
			runTest(t, distroSigningTest(t, spec, testConfig.Target.Package, testConfig), testenv.WithSolveStatusFn(handleStatus))

			assert.True(t, found, "Spec signing override warning message not emitted")
		})

		t.Run("with args", func(t *testing.T) {
			t.Parallel()

			spec := newSigningSpec()
			spec.PackageConfig.Signer.Args = map[string]string{
				"HELLO": "world",
				"FOO":   "bar",
			}
			runTest(t, distroSigningTest(t, spec, testConfig.Target.Package, testConfig))
		})

		t.Run("with path build arg and build context", func(t *testing.T) {
			t.Parallel()

			spec := newSigningSpec()
			spec.PackageConfig.Signer = nil

			signConfig := llb.Scratch().File(llb.Mkfile("/unusual_place.yml", 0o400, []byte(`
signer:
  image: `+phonySignerRef+`
  cmdline: /signer
`)))

			runTest(t, distroSigningTest(
				t,
				spec,
				testConfig.Target.Package,
				testConfig,
				withBuildContext(ctx, t, "dalec_signing_config", signConfig),
				withBuildArg("DALEC_SIGNING_CONFIG_CONTEXT_NAME", "dalec_signing_config"),
				withBuildArg("DALEC_SIGNING_CONFIG_PATH", "/unusual_place.yml"),
			))
		})

		t.Run("path build arg takes precedence over spec config", func(t *testing.T) {
			t.Parallel()

			spec := newSigningSpec()
			spec.PackageConfig.Signer.Frontend.Image = "notexist"

			signConfig := llb.Scratch().File(llb.Mkfile("/unusual_place.yml", 0o400, []byte(`
signer:
  image: `+phonySignerRef+`
  cmdline: /signer
`)))

			var found bool
			handleStatus := func(status *client.SolveStatus) {
				if found || status == nil {
					return
				}
				for _, w := range status.Warnings {
					if strings.Contains(string(w.Short), "Spec signing config overwritten") {
						found = true
						return
					}
				}
			}

			runTest(t,
				distroSigningTest(
					t,
					spec,
					testConfig.Target.Package,
					testConfig,
					withBuildContext(ctx, t, "dalec_signing_config", signConfig),
					withBuildArg("DALEC_SIGNING_CONFIG_CONTEXT_NAME", "dalec_signing_config"),
					withBuildArg("DALEC_SIGNING_CONFIG_PATH", "/unusual_place.yml"),
				),
				testenv.WithSolveStatusFn(handleStatus),
			)

			assert.True(t, found, "Signing overwritten warning message not emitted")
		})

		t.Run("with path build arg and build context", func(t *testing.T) {
			t.Parallel()

			spec := newSigningSpec()
			spec.PackageConfig.Signer = nil

			signConfig := llb.Scratch().File(llb.Mkfile("/unusual_place.yml", 0o400, []byte(`
signer:
  image: `+phonySignerRef+`
  cmdline: /signer
`)))

			runTest(t, distroSigningTest(
				t,
				spec,
				testConfig.Target.Package,
				testConfig,
				withBuildContext(ctx, t, "dalec_signing_config", signConfig),
				withBuildArg("DALEC_SIGNING_CONFIG_CONTEXT_NAME", "dalec_signing_config"),
				withBuildArg("DALEC_SIGNING_CONFIG_PATH", "/unusual_place.yml"),
			))
		})

		t.Run("with no build context and config path build arg", func(t *testing.T) {
			t.Parallel()

			spec := newSigningSpec()
			spec.PackageConfig.Signer = nil

			signConfig := llb.Scratch().
				File(llb.Mkfile("/sign_config.yml", 0o400, []byte(`
signer:
  image: `+phonySignerRef+`
  cmdline: /signer
`)))

			runTest(t, distroSigningTest(
				t,
				spec,
				testConfig.Target.Package,
				testConfig,
				withMainContext(ctx, t, signConfig),
				withBuildArg("DALEC_SIGNING_CONFIG_PATH", "/sign_config.yml"),
			))
		})

		t.Run("local context with config path takes precedence over spec", func(t *testing.T) {
			t.Parallel()

			spec := newSigningSpec()
			spec.PackageConfig.Signer.Frontend.Image = "notexist"

			signConfig := llb.Scratch().
				File(llb.Mkfile("/sign_config.yml", 0o400, []byte(`
signer:
  image: `+phonySignerRef+`
  cmdline: /signer
`)))
			var found bool
			handleStatus := func(status *client.SolveStatus) {
				if found || status == nil {
					return
				}
				for _, w := range status.Warnings {
					if strings.Contains(string(w.Short), "Spec signing config overwritten by config at path") {
						found = true
						return
					}
				}
			}

			runTest(t, distroSigningTest(
				t,
				spec,
				testConfig.Target.Package,
				testConfig,
				withMainContext(ctx, t, signConfig),
				withBuildArg("DALEC_SIGNING_CONFIG_PATH", "/sign_config.yml"),
			), testenv.WithSolveStatusFn(handleStatus))

			assert.True(t, found, "spec signing config overwritten warning not emitted")
		})

		t.Run("skip signing", func(t *testing.T) {
			t.Parallel()

			spec := newSigningSpec()
			var found bool
			handleStatus := func(status *client.SolveStatus) {
				if found || status == nil {
					return
				}
				for _, w := range status.Warnings {
					if strings.Contains(string(w.Short), "Signing disabled by build-arg") {
						found = true
						return
					}
				}
			}
			runTest(t, distroSkipSigningTest(t, spec, testConfig.Target.Package), testenv.WithSolveStatusFn(handleStatus))
			assert.True(t, found, "Signing disabled warning message not emitted")
		})

		t.Run("skip signing takes precedence over custom context", func(t *testing.T) {
			t.Parallel()

			spec := newSigningSpec()
			spec.PackageConfig.Signer = nil

			signConfig := llb.Scratch().
				File(llb.Mkfile("/sign_config.yml", 0o400, []byte(`
signer:
  image: `+phonySignerRef+`
  cmdline: /signer
`)))

			var found bool
			handleStatus := func(status *client.SolveStatus) {
				if found || status == nil {
					return
				}
				for _, w := range status.Warnings {
					if strings.Contains(string(w.Short), "Signing disabled by build-arg") {
						found = true
						return
					}
				}
			}

			runTest(t, distroSkipSigningTest(
				t,
				spec,
				testConfig.Target.Package,
				withBuildArg("DALEC_SIGNING_CONFIG_CONTEXT_NAME", "dalec_signing_config"),
				withBuildArg("DALEC_SIGNING_CONFIG_PATH", "/sign_config.yml"),
				withBuildContext(ctx, t, "dalec_signing_config", signConfig),
			), testenv.WithSolveStatusFn(handleStatus))

			assert.True(t, found, "Signing disabled warning message not emitted")
		})

		t.Run("skip signing takes precedence over local context", func(t *testing.T) {
			t.Parallel()

			spec := newSigningSpec()
			spec.PackageConfig.Signer = nil

			signConfig := llb.Scratch().
				File(llb.Mkfile("/sign_config.yml", 0o400, []byte(`
signer:
  image: `+phonySignerRef+`
  cmdline: /signer
`)))

			var found bool
			handleStatus := func(status *client.SolveStatus) {
				if found || status == nil {
					return
				}
				for _, w := range status.Warnings {
					if strings.Contains(string(w.Short), "Signing disabled by build-arg") {
						found = true
						return
					}
				}
			}

			runTest(t, distroSkipSigningTest(
				t,
				spec,
				testConfig.Target.Package,
				withMainContext(ctx, t, signConfig),
				withBuildArg("DALEC_SIGNING_CONFIG_PATH", "/sign_config.yml"),
			), testenv.WithSolveStatusFn(handleStatus))

			assert.True(t, found, "Signing disabled warning message not emitted")
		})
	}
}

func windowsSigningTests(t *testing.T, tcfg targetConfig) {
	t.Parallel()
	runBuild := func(ctx context.Context, t *testing.T, gwc gwclient.Client, spec *dalec.Spec, srOpts ...srOpt) {
		st := prepareWindowsSigningState(ctx, t, gwc, spec, srOpts...)

		def, err := st.Marshal(ctx)
		if err != nil {
			t.Fatal(err)
		}

		res := solveT(ctx, t, gwc, gwclient.SolveRequest{
			Definition: def.ToPB(),
		})

		tgt := readFile(ctx, t, "target", res)
		cfg := readFile(ctx, t, "config.json", res)
		mfst := readFile(ctx, t, "manifest.json", res)

		assert.Equal(t, string(tgt), "windowscross")
		assert.Contains(t, string(cfg), "windows")

		var files []string
		assert.NoError(t, json.Unmarshal(mfst, &files))
		slices.Sort(files)

		expectedFiles := tcfg.ListExpectedSignFiles(spec, platforms.DefaultSpec())
		slices.Sort(expectedFiles)
		assert.Equal(t, expectedFiles, files)
	}
	t.Run("target spec config", func(t *testing.T) {
		t.Parallel()
		runTest(t, func(ctx context.Context, gwc gwclient.Client) {
			spec := newSimpleSpec()
			spec.Targets = map[string]dalec.Target{
				"windowscross": {
					PackageConfig: &dalec.PackageConfig{
						Signer: &dalec.PackageSigner{
							Frontend: &dalec.Frontend{
								Image: phonySignerRef,
							},
						},
					},
				},
			}

			runBuild(ctx, t, gwc, spec)
		})
	})

	t.Run("root spec config", func(t *testing.T) {
		t.Parallel()
		runTest(t, func(ctx context.Context, gwc gwclient.Client) {
			spec := newSimpleSpec()
			spec.PackageConfig = &dalec.PackageConfig{
				Signer: &dalec.PackageSigner{
					Frontend: &dalec.Frontend{
						Image: phonySignerRef,
					},
				},
			}

			runBuild(ctx, t, gwc, spec)
		})
	})

	t.Run("with path arg and build context", func(t *testing.T) {
		t.Parallel()
		runTest(t, func(ctx context.Context, gwc gwclient.Client) {
			spec := newSimpleSpec()

			signConfig := llb.Scratch().File(llb.Mkfile("/unusual_place.yml", 0o400, []byte(`
signer:
  image: `+phonySignerRef+`
  cmdline: /signer
`)))

			runBuild(
				ctx,
				t,
				gwc,
				spec,
				withBuildArg("DALEC_SIGNING_CONFIG_CONTEXT_NAME", "dalec_signing_config"),
				withBuildArg("DALEC_SIGNING_CONFIG_PATH", "unusual_place.yml"),
				withBuildContext(ctx, t, "dalec_signing_config", signConfig),
			)
		})
	})

	t.Run("with path arg and local context", func(t *testing.T) {
		t.Parallel()
		runTest(t, func(ctx context.Context, gwc gwclient.Client) {
			spec := newSimpleSpec()

			signConfig := llb.Scratch().
				File(llb.Mkfile("/sign_config.yml", 0o400, []byte(`
signer:
  image: `+phonySignerRef+`
  cmdline: /signer
`)))

			runBuild(ctx,
				t,
				gwc,
				spec,
				withMainContext(ctx, t, signConfig),
				withBuildArg("DALEC_SIGNING_CONFIG_PATH", "/sign_config.yml"),
			)
		})

	})

	t.Run("test skipping windows signing", func(t *testing.T) {
		t.Parallel()

		var found bool
		handleStatus := func(status *client.SolveStatus) {
			if found || status == nil {
				return
			}
			for _, w := range status.Warnings {
				if strings.Contains(string(w.Short), "Signing disabled by build-arg") {
					found = true
					return
				}
			}
		}

		runTest(t, func(ctx context.Context, gwc gwclient.Client) {
			spec := newSimpleSpec()
			st := prepareWindowsSigningState(ctx, t, gwc, spec, withBuildArg("DALEC_SKIP_SIGNING", "1"))

			def, err := st.Marshal(ctx)
			if err != nil {
				t.Fatal(err)
			}

			res := solveT(ctx, t, gwc, gwclient.SolveRequest{
				Definition: def.ToPB(),
			})

			if _, err := maybeReadFile(ctx, "/target", res); err == nil {
				t.Fatalf("signing took place even though signing was disabled")
			}

			if _, err = maybeReadFile(ctx, "/config.json", res); err == nil {
				t.Fatalf("signing took place even though signing was disabled")
			}
		}, testenv.WithSolveStatusFn(handleStatus))

		assert.True(t, found, "Signing disabled warning message not emitted")
	})
}

func distroSigningTest(t *testing.T, spec *dalec.Spec, buildTarget string, tcfg testLinuxConfig, extraSrOpts ...srOpt) testenv.TestFunc {
	return func(ctx context.Context, gwc gwclient.Client) {
		topTgt, _, _ := strings.Cut(buildTarget, "/")

		srOpts := []srOpt{
			withSpec(ctx, t, spec),
			withBuildTarget(buildTarget),
		}
		srOpts = append(srOpts, extraSrOpts...)

		sr := newSolveRequest(srOpts...)
		res := solveT(ctx, t, gwc, sr)

		tgt := readFile(ctx, t, "/target", res)
		cfg := readFile(ctx, t, "/config.json", res)
		mfst := readFile(ctx, t, "/manifest.json", res)

		if string(tgt) != topTgt {
			t.Fatal(fmt.Errorf("target incorrect; either not sent to signer or not received back from signer"))
		}

		if !strings.Contains(string(cfg), "linux") {
			t.Fatal(fmt.Errorf("configuration incorrect"))
		}

		if spec.PackageConfig != nil && spec.PackageConfig.Signer != nil {
			for k, v := range spec.PackageConfig.Signer.Args {
				dt := readFile(ctx, t, "/env/"+k, res)
				assert.Equal(t, v, string(dt))
			}
		}

		if tcfg.Target.ListExpectedSignFiles == nil {
			t.Fatal("missing function to get list of expected files to sign")
		}

		expectedFiles := tcfg.Target.ListExpectedSignFiles(spec, platforms.DefaultSpec())
		slices.Sort(expectedFiles)

		var files []string
		assert.NoError(t, json.Unmarshal(mfst, &files))
		slices.Sort(files)

		assert.Equal(t, expectedFiles, files)
	}
}

func distroSkipSigningTest(t *testing.T, spec *dalec.Spec, buildTarget string, extraSrOpts ...srOpt) testenv.TestFunc {
	return func(ctx context.Context, gwc gwclient.Client) {
		srOpts := []srOpt{withSpec(ctx, t, spec), withBuildTarget(buildTarget), withBuildArg("DALEC_SKIP_SIGNING", "1")}
		srOpts = append(srOpts, extraSrOpts...)
		sr := newSolveRequest(srOpts...)

		res := solveT(ctx, t, gwc, sr)

		if _, err := maybeReadFile(ctx, "/target", res); err == nil {
			t.Fatalf("signer signed even though signing was disabled")
		}
		if _, err := maybeReadFile(ctx, "/config.json", res); err == nil {
			t.Fatalf("signer signed even though signing was disabled")
		}
	}
}
