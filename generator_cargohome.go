package dalec

import (
	"context"
	"path/filepath"

	"github.com/goccy/go-yaml/ast"
	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"
)

const (
	cargoHomeDir = "/cargo/registry"
)

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

func withCargohome(g *SourceGenerator, srcSt, worker llb.State, subPath string, opts ...llb.ConstraintsOpt) func(llb.State) llb.State {
	return func(in llb.State) llb.State {
		workDir := "/work/src"
		joinedWorkDir := filepath.Join(workDir, subPath, g.Subpath)
		srcMount := llb.AddMount(workDir, srcSt)

		paths := g.Cargohome.Paths
		if g.Cargohome.Paths == nil {
			paths = []string{"."}
		}

		for _, path := range paths {
			in = worker.Run(
				ShArgs("cargo fetch"),
				llb.AddEnv("CARGO_HOME", cargoHomeDir),
				llb.Dir(filepath.Join(joinedWorkDir, path)),
				srcMount,
				WithConstraints(opts...),
				g.Cargohome._sourceMap.GetLocation(in),
			).AddMount(cargoHomeDir, in)
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
					in = in.With(withCargohome(gen, patched[key], worker, key, opts...))
				}
			}
			return in
		})
	}

	return &deps
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
