package dalec

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/moby/buildkit/client/llb"
)

func TestCargoRegistryConfig(t *testing.T) {
	t.Parallel()

	cfg, err := cargoRegistryConfigFor(SourceOpts{BuildArgs: map[string]string{
		BuildArgDalecCargoRegistry:     "sparse+https://pkgs.example.test/Feed/Cargo/index",
		BuildArgDalecCargoRegistryName: "test-feed",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.tokenEnv != "CARGO_REGISTRIES_TEST_FEED_TOKEN" {
		t.Fatalf("unexpected Cargo token environment variable %q", cfg.tokenEnv)
	}
	if cfg.indexURL != "sparse+https://pkgs.example.test/Feed/Cargo/index/" {
		t.Fatalf("unexpected normalized Cargo registry URL %q", cfg.indexURL)
	}
	config := string(cfg.configFile())
	for _, expected := range []string{
		`[registries."test-feed"]`,
		`index = "sparse+https://pkgs.example.test/Feed/Cargo/index/"`,
		`replace-with = "test-feed"`,
		`[source."test-feed"]`,
	} {
		if !strings.Contains(config, expected) {
			t.Fatalf("Cargo config missing %q:\n%s", expected, config)
		}
	}
}

func TestCargoRegistryConfigRejectsReservedCratesIOName(t *testing.T) {
	t.Parallel()

	_, err := cargoRegistryConfigFor(SourceOpts{BuildArgs: map[string]string{
		BuildArgDalecCargoRegistry:     "https://pkgs.example.test/feed",
		BuildArgDalecCargoRegistryName: "crates-io",
	}})
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("expected reserved registry name error, got %v", err)
	}
}

func TestCargoRegistryConfigRejectsInsecureURL(t *testing.T) {
	t.Parallel()

	_, err := cargoRegistryConfigFor(SourceOpts{BuildArgs: map[string]string{
		BuildArgDalecCargoRegistry: "http://pkgs.example.test/feed",
	}})
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("expected https validation error, got %v", err)
	}
}

func TestCargoRegistryConfigRejectsURLCredentials(t *testing.T) {
	t.Parallel()

	_, err := cargoRegistryConfigFor(SourceOpts{BuildArgs: map[string]string{
		BuildArgDalecCargoRegistry: "https://user:password@pkgs.example.test/feed",
	}})
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("expected URL validation error, got %v", err)
	}
}

func TestCargohomeDepsUsesConfiguredRegistryForFetch(t *testing.T) {
	t.Parallel()

	spec := testCargohomeSpec()
	sOpt := SourceOpts{BuildArgs: map[string]string{
		BuildArgDalecCargoRegistry:     "https://pkgs.example.test/feed",
		BuildArgDalecCargoRegistryName: "test-feed",
	}}
	st := spec.CargohomeDeps(sOpt, llb.Scratch())
	if st == nil {
		t.Fatal("cargohome generator returned nil state")
	}

	var sawFetch, sawConfig, sawSecret bool
	for _, op := range sourceOpsFromState(context.Background(), t, *st) {
		exec := op.GetExec()
		if exec != nil && slices.Contains(exec.Meta.Env, "CARGO_HOME=/cargo/registry") {
			args := strings.Join(exec.Meta.Args, " ")
			if !strings.Contains(args, "cargo fetch") {
				t.Fatalf("expected configured Cargo fetch command, got %q", args)
			}
			if !strings.Contains(args, "if [ -s "+cargoRegistrySecretPath+" ]; then") {
				t.Fatalf("expected Cargo fetch to work without a secret, got %q", args)
			}
			if !strings.Contains(args, "CARGO_REGISTRIES_TEST_FEED_TOKEN") {
				t.Fatalf("expected Cargo token to be read from the secret, got %q", args)
			}
			sawFetch = true
			for _, mount := range exec.Mounts {
				if mount.Dest == cargoRegistrySecretPath && mount.GetSecretOpt().GetID() == cargoRegistrySecretID {
					if !mount.GetSecretOpt().GetOptional() {
						t.Fatal("expected Cargo registry secret mount to be optional")
					}
					sawSecret = true
				}
			}
		}
		fileOp := op.GetFile()
		if fileOp == nil {
			continue
		}
		for _, action := range fileOp.Actions {
			mkfile := action.GetMkfile()
			if mkfile == nil || (mkfile.Path != "config.toml" && mkfile.Path != "/config.toml") {
				continue
			}
			if !strings.Contains(string(mkfile.Data), `[source.crates-io]`) {
				t.Fatalf("persisted Cargo config does not replace crates.io:\n%s", mkfile.Data)
			}
			sawConfig = true
		}
	}
	if !sawFetch {
		t.Fatal("expected Cargo dependency fetch exec")
	}
	if !sawSecret {
		t.Fatal("expected Cargo token BuildKit secret mount")
	}
	if !sawConfig {
		t.Fatal("expected Cargo registry config in generated Cargo home")
	}
}

func TestCargohomeDepsKeepsDefaultCargoFetch(t *testing.T) {
	t.Parallel()

	st := testCargohomeSpec().CargohomeDeps(SourceOpts{}, llb.Scratch())
	if st == nil {
		t.Fatal("cargohome generator returned nil state")
	}

	for _, op := range sourceOpsFromState(context.Background(), t, *st) {
		exec := op.GetExec()
		if exec == nil || !slices.Contains(exec.Meta.Env, "CARGO_HOME=/cargo/registry") {
			continue
		}
		args := strings.Join(exec.Meta.Args, " ")
		if args != "sh -c cargo fetch" {
			t.Fatalf("expected the default Cargo fetch command, got %q", args)
		}
		return
	}
	t.Fatal("expected Cargo dependency fetch exec")
}

func TestCargohomeDepsSurfacesInvalidRegistry(t *testing.T) {
	t.Parallel()

	st := testCargohomeSpec().CargohomeDeps(SourceOpts{BuildArgs: map[string]string{
		BuildArgDalecCargoRegistry: "http://pkgs.example.test/feed",
	}}, llb.Scratch())
	if st == nil {
		t.Fatal("cargohome generator returned nil state")
	}
	if _, err := st.Marshal(context.Background()); err == nil {
		t.Fatal("expected invalid Cargo registry to be surfaced while marshalling")
	}
}

func testCargohomeSpec() *Spec {
	return &Spec{Sources: map[string]Source{
		"src": {
			Git: &SourceGit{
				URL:    "https://example.com/repo.git",
				Commit: "0123456789abcdef",
			},
			Generate: []*SourceGenerator{{Cargohome: &GeneratorCargohome{}}},
		},
	}}
}
