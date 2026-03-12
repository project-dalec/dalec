package test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/test/testenv"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
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

			assert.Assert(t, found, "Spec signing override warning message not emitted")
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

			assert.Assert(t, found, "Signing overwritten warning message not emitted")
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

			assert.Assert(t, found, "spec signing config overwritten warning not emitted")
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
			assert.Assert(t, found, "Signing disabled warning message not emitted")
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

			assert.Assert(t, found, "Signing disabled warning message not emitted")
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

			assert.Assert(t, found, "Signing disabled warning message not emitted")
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

		assert.Check(t, cmp.Equal(string(tgt), "windowscross"))
		assert.Check(t, cmp.Contains(string(cfg), "windows"))

		var files []string
		assert.NilError(t, json.Unmarshal(mfst, &files))
		slices.Sort(files)

		expectedFiles := tcfg.ListExpectedSignFiles(spec, platforms.DefaultSpec())
		slices.Sort(expectedFiles)
		assert.Assert(t, cmp.DeepEqual(files, expectedFiles))
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

		assert.Assert(t, found, "Signing disabled warning message not emitted")
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
		assert.NilError(t, json.Unmarshal(mfst, &files))
		slices.Sort(files)

		assert.Assert(t, cmp.DeepEqual(files, expectedFiles))
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

// signRPMs signs all RPM files in the package state using a GPG key.
// The worker state must have rpmsign available (or tdnf/dnf to install it).
// The gpgKey state is expected to have a private key at /private.key
// (as produced by [generateGPGKey]).
// The pkgState is expected to have RPMs under /RPMS/<arch>/*.rpm
// (the standard package target output).
// It returns the modified package state with signed RPMs.
func signRPMs(worker llb.State, gpgKey llb.State, pkgState llb.State) llb.State {
	pg := dalec.ProgressGroup("Sign RPMs with GPG key")

	scriptDt := `#!/usr/bin/env bash
set -eux -o pipefail

if ! command -v rpmsign &> /dev/null; then
	if command -v tdnf &> /dev/null; then
		tdnf install -y rpm-sign
	elif command -v dnf &> /dev/null; then
		dnf install -y rpm-sign
	fi
fi

gpg --import < /tmp/gpg/private.key
ID=$(gpg --list-keys --keyid-format LONG | awk '/^pub/{print $2}' | cut -d/ -f2 | head -1)

echo "%_gpg_name $ID" > ~/.rpmmacros

find /tmp/rpms/RPMS -name "*.rpm" -exec rpmsign --addsign {} \;
`

	script := llb.Scratch().File(
		llb.Mkfile("/script.sh", 0o755, []byte(scriptDt)),
		pg,
	)

	return worker.Run(
		llb.AddMount("/tmp/signing", script, llb.Readonly),
		llb.AddMount("/tmp/gpg", gpgKey, llb.Readonly),
		llb.AddMount("/tmp/rpms", pkgState),
		dalec.ShArgs("/tmp/signing/script.sh"),
		pg,
	).GetMount("/tmp/rpms")
}

// testSignedRPMCustomBaseImage tests that signed RPMs can be installed into
// a container with a custom base image.
//
// This reproduces a bug where the tdnfrepogpgcheck plugin rejects signed RPMs
// installed via "tdnf install /path/to/signed.rpm --installroot=/tmp/rootfs"
// because the @cmdline virtual repo has no gpgkey entry.
//
// The distroImageRef parameter is the image reference for the distro's base
// image (e.g., azlinux.Azlinux3Ref), which is used as the custom base image
// in the spec.
func testSignedRPMCustomBaseImage(ctx context.Context, t *testing.T, targetCfg targetConfig, distroImageRef string) {
	t.Run("signed rpm with custom base image", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			// Get the worker state — we need it to generate GPG keys and sign RPMs.
			sr := newSolveRequest(withBuildTarget(targetCfg.Worker), withSpec(ctx, t, nil))
			w := reqToState(ctx, client, sr, t)

			// Generate a GPG key pair for signing.
			gpgKey := generateGPGKey(w, true)

			// Create a simple spec and build the RPM package.
			spec := newSimpleSpec()
			pkgSr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(targetCfg.Package))
			pkgSt := reqToState(ctx, client, pkgSr, t)

			// Sign the RPMs on the worker using rpmsign --addsign.
			signedPkgSt := signRPMs(w, gpgKey, pkgSt)

			// Create a container spec with a custom base image.
			// This triggers skipBase=true in BuildContainer, meaning the RPMs
			// are installed via "tdnf install /path/to/signed.rpm --installroot=/tmp/rootfs"
			// into the custom base image's rootfs.
			spec.Image = &dalec.ImageConfig{
				Entrypoint: "/usr/bin/foo",
				Bases: []dalec.BaseImage{
					{
						Rootfs: dalec.Source{
							DockerImage: &dalec.SourceDockerImage{
								Ref: distroImageRef,
							},
						},
					},
				},
			}

			containerSr := newSolveRequest(
				withSpec(ctx, t, spec),
				withBuildTarget(targetCfg.Container),
				withBuildContext(ctx, t, dalec.GenericPkg, signedPkgSt),
			)

			solveT(ctx, t, client, containerSr)
		})
	})
}
