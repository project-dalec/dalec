package dalec

import (
	"context"
	"path/filepath"

	"github.com/goccy/go-yaml/ast"
	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"
)

func (s *Source) isPip() bool {
	for _, gen := range s.Generate {
		if gen.Pip != nil {
			return true
		}
	}
	return false
}

func (s *Spec) HasPips() bool {
	for _, src := range s.Sources {
		if src.isPip() {
			return true
		}
	}
	return false
}

func withPip(g *SourceGenerator, srcSt, worker llb.State, path string, opts ...llb.ConstraintsOpt) llb.State {
	workDir := "/work/src"
	joinedWorkDir := filepath.Join(workDir, path, g.Subpath)
	srcMount := llb.AddMount(workDir, srcSt)

	paths := g.Pip.Paths
	if g.Pip.Paths == nil {
		paths = []string{"."}
	}

	// Create a cache directory for pip packages
	result := llb.Scratch()
	cacheDir := "/pip-cache"

	for _, path := range paths {
		requirementsFile := g.Pip.RequirementsFile
		if requirementsFile == "" {
			requirementsFile = "requirements.txt"
		}

		pipCmd := "set -e; "

		// Create the cache directory
		pipCmd += "mkdir -p " + cacheDir + "; "

		// First, download essential build dependencies that are needed for source builds
		pipCmd += "python3 -m pip download --dest=" + cacheDir + " setuptools wheel"

		if g.Pip.IndexUrl != "" {
			pipCmd += " --index-url=" + g.Pip.IndexUrl
		}
		for _, extraUrl := range g.Pip.ExtraIndexUrls {
			pipCmd += " --extra-index-url=" + extraUrl
		}

		pipCmd += "; python3 -m pip download --no-binary=:all: --dest=" + cacheDir + " --requirement=" + requirementsFile

		// Add custom index URLs for main dependencies if specified
		if g.Pip.IndexUrl != "" {
			pipCmd += " --index-url=" + g.Pip.IndexUrl
		}
		for _, extraUrl := range g.Pip.ExtraIndexUrls {
			pipCmd += " --extra-index-url=" + extraUrl
		}

		// Just download the packages, don't install them
		result = worker.Run(
			llb.Args([]string{"bash", "-c", pipCmd}),
			llb.Dir(filepath.Join(joinedWorkDir, path)),
			srcMount,
			WithConstraints(opts...),
			g.Pip._sourceMap.GetLocation(result),
		).AddMount(cacheDir, result)
	}
	return result
}

func (s *Spec) pipSources() map[string]Source {
	sources := map[string]Source{}
	for name, src := range s.Sources {
		if src.isPip() {
			sources[name] = src
		}
	}
	return sources
}

// PipDeps returns an llb.State containing all the pip dependencies for the spec
// for any sources that have a pip generator specified.
// If there are no sources with a pip generator, this will return nil.
// The returned state contains a merged cache of all downloaded pip packages.
func (s *Spec) PipDeps(sOpt SourceOpts, worker llb.State, opts ...llb.ConstraintsOpt) *llb.State {
	sources := s.pipSources()
	if len(sources) == 0 {
		return nil
	}

	// Get the patched sources for the Python projects
	// This is needed in case a patch includes changes to requirements.txt
	patched := s.getPatchedSources(sOpt, worker, func(name string) bool {
		_, ok := sources[name]
		return ok
	}, opts...)

	// Create a unified cache containing all pip packages
	var cacheStates []llb.State
	sorted := SortMapKeys(patched)
	opts = append(opts, ProgressGroup("Fetch pip dependencies for sources"))
	for _, key := range sorted {
		src := s.Sources[key]
		merged := patched[key]
		for _, gen := range src.Generate {
			if gen.Pip == nil {
				continue
			}
			cacheState := withPip(gen, merged, worker, key, opts...)
			cacheStates = append(cacheStates, cacheState)
		}
	}

	if len(cacheStates) == 0 {
		return nil
	}

	// Merge all cache states into a single state
	merged := MergeAtPath(llb.Scratch(), cacheStates, "/", opts...)
	return &merged
}

func (gen *GeneratorPip) UnmarshalYAML(ctx context.Context, node ast.Node) error {
	type internal GeneratorPip
	var i internal

	dec := getDecoder(ctx)
	if err := dec.DecodeFromNodeContext(ctx, node, &i); err != nil {
		return errors.Wrap(err, "failed to decode pip generator")
	}

	*gen = GeneratorPip(i)
	gen._sourceMap = newSourceMap(ctx, node)
	return nil
}
