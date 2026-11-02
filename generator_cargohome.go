package dalec

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml/ast"
	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"
)

const (
	cargoHomeDir = "/cargo/registry"
	// cargoRegistrySecretID is the stable BuildKit secret ID used for
	// authenticated Cargo registry fetches. The Cargo environment variable
	// remains registry-specific, but callers should not need to derive the
	// BuildKit secret ID from the registry name.
	cargoRegistrySecretID   = "dalec-cargo-token"
	cargoRegistrySecretPath = "/run/secrets/" + cargoRegistrySecretID

	// DefaultCargoRegistryName is used when a build supplies a Cargo registry
	// URL without explicitly supplying a registry name.
	DefaultCargoRegistryName = "dalec-cargo"

	// BuildArgDalecCargoRegistry is an optional frontend build arg containing
	// the Cargo registry index URL. When it is empty, Cargo keeps its normal
	// configuration and crates.io behavior.
	BuildArgDalecCargoRegistry = "DALEC_CARGO_REGISTRY"

	// BuildArgDalecCargoRegistryName is the optional Cargo registry name used
	// with BuildArgDalecCargoRegistry.
	BuildArgDalecCargoRegistryName = "DALEC_CARGO_REGISTRY_NAME"
)

type cargoRegistryConfig struct {
	name     string
	indexURL string
	tokenEnv string
}

// CargoRegistryTokenEnv returns the Cargo environment variable used for the
// token of a named registry. Cargo normalizes registry names by upper-casing
// them and replacing hyphens with underscores.
func CargoRegistryTokenEnv(name string) string {
	return "CARGO_REGISTRIES_" + strings.NewReplacer("-", "_").Replace(strings.ToUpper(name)) + "_TOKEN"
}

func cargoRegistryConfigFor(sOpt SourceOpts) (cargoRegistryConfig, error) {
	registryURL := sOpt.BuildArgs[BuildArgDalecCargoRegistry]
	if registryURL == "" {
		return cargoRegistryConfig{}, nil
	}

	name := sOpt.BuildArgs[BuildArgDalecCargoRegistryName]
	if name == "" {
		name = DefaultCargoRegistryName
	}
	if !validCargoRegistryName(name) {
		return cargoRegistryConfig{}, fmt.Errorf("invalid cargo registry name %q", name)
	}
	if strings.TrimSpace(registryURL) != registryURL || strings.ContainsAny(registryURL, "\r\n") {
		return cargoRegistryConfig{}, fmt.Errorf("invalid Cargo registry URL %q", registryURL)
	}

	indexURL := strings.TrimPrefix(registryURL, "sparse+")
	u, err := url.Parse(indexURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return cargoRegistryConfig{}, fmt.Errorf("cargo registry URL must be an https URL, got %q", registryURL)
	}

	return cargoRegistryConfig{
		name:     name,
		indexURL: "sparse+" + u.String(),
		tokenEnv: CargoRegistryTokenEnv(name),
	}, nil
}

func validCargoRegistryName(name string) bool {
	if name == "" {
		return false
	}
	if !cargoRegistryNameLetter(rune(name[0])) {
		return false
	}
	for _, r := range name[1:] {
		if cargoRegistryNameChar(r) {
			continue
		}
		return false
	}
	return true
}

func cargoRegistryNameLetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func cargoRegistryNameChar(r rune) bool {
	return cargoRegistryNameLetter(r) || (r >= '0' && r <= '9') || r == '-' || r == '_'
}

func (c cargoRegistryConfig) configFile() []byte {
	name := strconv.Quote(c.name)
	indexURL := strconv.Quote(c.indexURL)
	return []byte(fmt.Sprintf(`[registries.%s]
index = %s

[source.crates-io]
replace-with = %s

[source.%s]
registry = %s
`, name, indexURL, name, name, indexURL))
}

func (s *Source) isCargohome() bool {
	for _, gen := range s.Generate {
		if gen.Cargohome != nil {
			return true
		}
	}
	return false
}

// HasCargohomes returns true if any of the sources in the spec are a Rust Cargo project.
func (s *Spec) HasCargohomes() bool {
	for _, src := range s.Sources {
		if src.isCargohome() {
			return true
		}
	}
	return false
}

func withCargohome(g *SourceGenerator, sOpt SourceOpts, srcSt, worker llb.State, subPath string, opts ...llb.ConstraintsOpt) func(llb.State) llb.State {
	return func(in llb.State) llb.State {
		cargoConfig, err := cargoRegistryConfigFor(sOpt)
		if err != nil {
			return ErrorState(in, err)
		}

		workDir := "/work/src"
		joinedWorkDir := filepath.Join(workDir, subPath, g.Subpath)
		srcMount := llb.AddMount(workDir, srcSt)

		paths := g.Cargohome.Paths
		if g.Cargohome.Paths == nil {
			paths = []string{"."}
		}
		if cargoConfig.name != "" {
			// Keep the registry mapping with the generated Cargo home. The
			// package build reuses this directory with CARGO_NET_OFFLINE=true,
			// so Cargo must retain the same source replacement it used for fetch.
			in = in.File(llb.Mkfile("config.toml", 0o600, cargoConfig.configFile()), WithConstraints(opts...))
		}

		for _, path := range paths {
			runOpts := []llb.RunOption{
				llb.AddEnv("CARGO_HOME", cargoHomeDir),
				llb.Dir(filepath.Join(joinedWorkDir, path)),
				srcMount,
				WithConstraints(opts...),
				g.Cargohome._sourceMap.GetLocation(in),
			}
			fetchCommand := "cargo fetch"
			if cargoConfig.name != "" {
				runOpts = append(runOpts, cargoRegistrySecret(cargoRegistrySecretID, cargoRegistrySecretPath))
				fetchCommand = fmt.Sprintf("set -eu; export %s=\"$(cat %s)\"; export %s=cargo:token; cargo fetch", cargoConfig.tokenEnv, cargoRegistrySecretPath, strings.TrimSuffix(cargoConfig.tokenEnv, "_TOKEN")+"_CREDENTIAL_PROVIDER")
			}

			in = worker.Run(append(runOpts, ShArgs(fetchCommand))...).AddMount(cargoHomeDir, in)
		}
		return in
	}
}

func (s *Spec) cargohomeSources() map[string]Source {
	sources := map[string]Source{}
	for name, src := range s.Sources {
		if src.isCargohome() {
			sources[name] = src
		}
	}
	return sources
}

// CargohomeDeps returns an [llb.State] containing all the Cargo dependencies for the spec
// for any sources that have a cargohome generator specified.
// If there are no sources with a cargohome generator, this will return a nil state.
func (s *Spec) CargohomeDeps(sOpt SourceOpts, worker llb.State, opts ...llb.ConstraintsOpt) *llb.State {
	sources := s.cargohomeSources()
	if len(sources) == 0 {
		return nil
	}

	deps := llb.Scratch()

	// Get the patched sources for the Cargo projects
	// This is needed in case a patch includes changes to Cargo.toml or Cargo.lock
	patched := s.getPatchedSources(sOpt, worker, func(name string) bool {
		_, ok := sources[name]
		return ok
	}, opts...)

	sorted := SortMapKeys(patched)

	for _, key := range sorted {
		src := s.Sources[key]

		opts := append(opts, ProgressGroup("Fetch Cargo dependencies for source: "+key))
		deps = deps.With(func(in llb.State) llb.State {
			for _, gen := range src.Generate {
				if gen.Cargohome != nil {
					in = in.With(withCargohome(gen, sOpt, patched[key], worker, key, opts...))
				}
			}
			return in
		})
	}

	deps = deps.With(sourceFilter(sOpt, opts...))
	return &deps
}

func cargoRegistrySecret(secretID, secretPath string) llb.RunOption {
	return runOptionFunc(func(ei *llb.ExecInfo) {
		llb.AddSecret(secretPath, llb.SecretID(secretID)).SetRunOption(ei)
	})
}

func (gen *GeneratorCargohome) UnmarshalYAML(ctx context.Context, node ast.Node) error {
	type internal GeneratorCargohome
	var i internal

	dec := getDecoder(ctx)
	if err := dec.DecodeFromNodeContext(ctx, node, &i); err != nil {
		return errors.Wrap(err, "failed to decode cargohome generator")
	}

	*gen = GeneratorCargohome(i)
	gen._sourceMap = newSourceMap(ctx, node)
	return nil
}
