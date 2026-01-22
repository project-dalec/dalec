package test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/project-dalec/dalec"
)

func createRepoSuffix() string {
	buf := make([]byte, 8)
	n, _ := rand.Read(buf)
	return hex.EncodeToString(buf[:n])
}

func testCustomRepo(ctx context.Context, t *testing.T, workerCfg workerConfig, targetCfg targetConfig) {
	// provide a unique suffix per test otherwise, depending on the test case,
	// you can end up with a false positive result due to apt package caching.
	// e.g. there may not be a public key for the repo under test, but if the
	// package is already in the package cache (due to other tests that injected
	// a public key) then apt may use that package anyway.

	getDepSpec := func(suffix string) *dalec.Spec {
		return &dalec.Spec{
			Name:        "dalec-test-package" + suffix,
			Version:     "0.0.1",
			Revision:    "1",
			Description: "A basic package for various testing uses",
			License:     "MIT",
			Sources: map[string]dalec.Source{
				"dalec-test-version": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "#!/usr/bin/env bash\necho \"version: 0.0.1\"",
							Permissions: 0o755,
						},
					},
				},
			},

			Artifacts: dalec.Artifacts{
				Binaries: map[string]dalec.ArtifactConfig{
					"dalec-test-version": {},
				},
			},
		}
	}

	getSpec := func(dep *dalec.Spec, keyConfig map[string]dalec.Source, repoPath, keyPath string) *dalec.Spec {
		spec := &dalec.Spec{
			Name:        "dalec-test-custom-repo",
			Version:     "0.0.1",
			Revision:    "1",
			Description: "Testing allowing a custom repo to be provided",
			License:     "MIT",
			Dependencies: &dalec.PackageDependencies{
				Build: map[string]dalec.PackageConstraints{
					dep.Name: {},
				},
				Runtime: map[string]dalec.PackageConstraints{
					dep.Name: {},
				},

				Test: map[string]dalec.PackageConstraints{
					dep.Name:    {},
					"bash":      {},
					"coreutils": {},
				},

				ExtraRepos: []dalec.PackageRepositoryConfig{
					{
						Config: workerCfg.TestRepoConfig(keyPath, repoPath),
						Data: []dalec.SourceMount{
							{
								Dest: repoPath,
								Spec: dalec.Source{
									Context: &dalec.SourceContext{
										Name: "test-repo",
									},
								},
							},
						},
						Keys: keyConfig,
						Envs: []string{"build", "install", "test"},
					},
				},
			},

			Build: dalec.ArtifactBuild{
				Steps: []dalec.BuildStep{
					{
						Command: `set -x; [ "$(dalec-test-version)" = "version: 0.0.1" ]`,
					},
				},
			},

			Tests: []*dalec.TestSpec{
				{
					Name: "Check test dependency installed from custom repo",
					// Dummy command here to force test steps to run and install test stage dependency
					// from custom repo
					Steps: []dalec.TestStep{
						{
							Command: "ls -lrt",
						},
					},
				},
			},
		}

		if workerCfg.Platform != nil && workerCfg.Platform.OS == "windows" {
			spec.Dependencies.Runtime = nil
			spec.Dependencies.Test = nil
			spec.Tests = nil
		}
		return spec
	}

	getRepoState := func(ctx context.Context, t *testing.T, client gwclient.Client, w llb.State, key llb.State, depSpec *dalec.Spec, repoPath string) llb.State {
		sr := newSolveRequest(withSpec(ctx, t, depSpec), withBuildTarget(targetCfg.Package))
		pkg := reqToState(ctx, client, sr, t)

		// create a repo using our existing worker
		workerWithRepo := w.With(workerCfg.CreateRepo(pkg, repoPath, workerCfg.SignRepo(key, repoPath)))

		// copy out just the contents of the repo
		return llb.Scratch().File(llb.Copy(workerWithRepo, repoPath, "/", &llb.CopyInfo{CopyDirContentsOnly: true}))
	}

	t.Run("no public key", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)

		testNoPublicKey := func(ctx context.Context, gwc gwclient.Client) {
			sr := newSolveRequest(withBuildTarget(targetCfg.Worker), withSpec(ctx, t, nil))
			w := reqToState(ctx, gwc, sr, t)

			// generate a gpg public/private key pair
			gpgKey := generateGPGKey(w, false)

			depSpec := getDepSpec("no-public-key")
			repoPath := filepath.Join("/opt/repo", createRepoSuffix())

			repoState := getRepoState(ctx, t, gwc, w, gpgKey, depSpec, repoPath)

			sr = newSolveRequest(
				withSpec(ctx, t, getSpec(depSpec, nil, repoPath, "public.key")),
				withBuildContext(ctx, t, "test-repo", repoState),
				withBuildTarget(targetCfg.Container),
				withPlatformPtr(workerCfg.Platform),
			)

			_, err := gwc.Solve(ctx, sr)
			if err == nil {
				t.Fatal("expected solve to fail")
			}
		}

		testEnv.RunTest(ctx, t, testNoPublicKey)
	})

	testWithPublicKey := func(t *testing.T, armored bool) func(context.Context, gwclient.Client) {
		return func(ctx context.Context, gwc gwclient.Client) {
			sr := newSolveRequest(withBuildTarget(targetCfg.Worker), withSpec(ctx, t, nil))
			w := reqToState(ctx, gwc, sr, t)

			packageNameSuffix := "with-public-key"
			if armored {
				packageNameSuffix += "-armored"
			}

			// generate a gpg key to sign the repo
			// under /public.gpg or /public.asc, depending on armored flag
			gpgKey := generateGPGKey(w, armored)
			depSpec := getDepSpec(packageNameSuffix)
			repoPath := filepath.Join("/opt/repo", createRepoSuffix())
			repoState := getRepoState(ctx, t, gwc, w, gpgKey, depSpec, repoPath)

			ext := ".gpg"
			if armored {
				ext = ".asc"
			}
			keyName := "public" + ext

			spec := getSpec(depSpec, map[string]dalec.Source{
				// in the dalec spec, the public key will be passed in via build context
				keyName: {
					Context: &dalec.SourceContext{
						Name: "repo-public-key",
					},
					Path: keyName,
				},
			}, repoPath, keyName)

			sr = newSolveRequest(
				withSpec(ctx, t, spec),
				withBuildContext(ctx, t, "test-repo", repoState),
				withBuildContext(ctx, t, "repo-public-key", gpgKey),
				withBuildTarget(targetCfg.Container),
				withPlatformPtr(workerCfg.Platform),
			)

			res := solveT(ctx, t, gwc, sr)
			_, err := res.SingleRef()
			if err != nil {
				t.Fatal(err)
			}
		}
	}

	t.Run("with public key armored", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)

		publicKeyArmored := testWithPublicKey(t, true)
		testEnv.RunTest(ctx, t, publicKeyArmored)
	})

	t.Run("with public key dearmored", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)

		publicKeyDearmored := testWithPublicKey(t, false)
		testEnv.RunTest(ctx, t, publicKeyDearmored)
	})
}

func generateGPGKey(worker llb.State, armored bool) llb.State {
	pg := dalec.ProgressGroup("Generate GPG Key for Testing")

	publicKeyExportCmd := "gpg --export"
	if armored {
		publicKeyExportCmd += " --armor"
	}

	ext := ".gpg"
	if armored {
		ext = ".asc"
	}

	st := worker.
		Run(dalec.ShArgs(`gpg --batch --gen-key <<EOF
Key-Type: RSA
Key-Length: 2048
Subkey-Type: RSA
Subkey-Length: 2048
Name-Real: Test User
Name-Comment: Test Key
Name-Email: test@example.com
Expire-Date: 0
%no-protection
%commit
EOF
		`), pg).
		Run(dalec.ShArgs(fmt.Sprintf(
			"set +ex; %s test@example.com > /tmp/gpg/public%s; gpg --export-secret-keys --armor test@example.com > /tmp/gpg/private.key",
			publicKeyExportCmd,
			ext)),
			pg).
		AddMount("/tmp/gpg", llb.Scratch())

	return st
}
