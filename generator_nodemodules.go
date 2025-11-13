package dalec

import (
	"context"
	"path/filepath"

	"github.com/goccy/go-yaml/ast"
	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"
)

func (s *Source) isNodeMod() bool {
	for _, gen := range s.Generate {
		if gen.NodeMod != nil {
			return true
		}
	}
	return false
}

// HasNodeMods returns true if any of the sources in the spec are node modules.
func (s *Spec) HasNodeMods() bool {
	for _, src := range s.Sources {
		if src.isNodeMod() {
			return true
		}
	}
	return false
}

func withNodeMod(g *SourceGenerator, worker llb.State, name string, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		workDir := "/work/src"
		joinedWorkDir := filepath.Join(workDir, name, g.Subpath)
		const installCmd = "npm install"
		const installBasePath = "/work/download"

		paths := g.NodeMod.Paths
		if g.NodeMod.Paths == nil {
			paths = []string{"."}
		}

		states := make([]llb.State, 0, len(paths))
		for _, path := range paths {
			// For each path, create an empty mount to store the downloaded packages
			// The final result with add a "node_modules" directory at the given path
			// To accomplish this, npm pip to download the packages to a similar
			// subpath so that we can just take the contents of the mount directly
			// without having to do an additional copy to move the files around.

			installPath := filepath.Join(installBasePath, name, g.Subpath, path)
			installCmd := installCmd + " --prefix " + installPath

			st := worker.Run(
				ShArgs(installCmd),
				llb.Dir(filepath.Join(joinedWorkDir, path)),
				WithConstraints(opts...),
				llb.AddMount(workDir, in, llb.Readonly),
				llb.IgnoreCache,
				g.NodeMod._sourceMap.GetLocation(in),
			).AddMount(installBasePath, in)

			states = append(states, st)
		}
		return MergeAtPath(llb.Scratch(), append(states, in), "/", opts...)
	}
}

func (s *Spec) nodeModSources() map[string]Source {
	sources := map[string]Source{}
	for name, src := range s.Sources {
		if src.isNodeMod() {
			sources[name] = src
		}
	}
	return sources
}

// NodeModDeps returns a map[string]llb.State containing all the node module dependencies for the spec
// for any sources that have a node module generator specified.
// If there are no sources with a node module generator, this will return nil.
// The returned states have node_modules installed for each relevant source, using sources as input.
func (s *Spec) NodeModDeps(sOpt SourceOpts, worker llb.State, opts ...llb.ConstraintsOpt) map[string]llb.State {
	sources := s.nodeModSources()
	if len(sources) == 0 {
		return nil
	}

	// Get the patched sources for the node modules
	patched := s.getPatchedSources(sOpt, worker, func(name string) bool {
		_, ok := sources[name]
		return ok
	}, opts...)

	result := make(map[string]llb.State)
	sorted := SortMapKeys(patched)
	opts = append(opts, ProgressGroup("Fetch node module dependencies for sources"))
	for _, key := range sorted {
		src := s.Sources[key]
		merged := patched[key]
		for _, gen := range src.Generate {
			if gen.NodeMod == nil {
				continue
			}
			merged = merged.With(withNodeMod(gen, worker, key, opts...))
		}
		result[key] = merged
	}
	return result
}

func (gen *GeneratorNodeMod) UnmarshalYAML(ctx context.Context, node ast.Node) error {
	type internal GeneratorNodeMod
	var i internal

	dec := getDecoder(ctx)
	if err := dec.DecodeFromNodeContext(ctx, node, &i); err != nil {
		return errors.Wrap(err, "failed to decode node module generator")
	}

	*gen = GeneratorNodeMod(i)
	gen._sourceMap = newSourceMap(ctx, node)
	return nil
}
