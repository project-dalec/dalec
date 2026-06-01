package dalec

import (
	"context"
	goerrors "errors"
	"fmt"
	"io"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
	"github.com/moby/buildkit/frontend/dockerui"
	"github.com/moby/buildkit/solver/errdefs"
)

// SourceBuild is used to generate source from a DockerFile build.
type SourceBuild struct {
	// A source specification to use as the context for the Dockerfile build
	Source Source `yaml:"source,omitempty" json:"source,omitempty"`

	// DockerfilePath is the path to the build file in the build context
	// If not set the default is assumed by buildkit to be `Dockerfile` at the root of the context.
	DockerfilePath string `yaml:"dockerfile_path,omitempty" json:"dockerfile_path,omitempty"`

	// Target specifies the build target to use.
	// If unset, the default target is determined by the frontend implementation
	// (e.g. the dockerfile frontend uses the last build stage as the default).
	Target string `yaml:"target,omitempty" json:"target,omitempty"`
	// Args are the build args to pass to the build.
	Args map[string]string `yaml:"args,omitempty" json:"args,omitempty"`

	_sourceMap *sourceMap `yaml:"-" json:"-"`
}

func (s *SourceBuild) validate(fetchOptions) error {
	var errs []error
	if s.Source.Build != nil {
		err := fmt.Errorf("build sources cannot be recursive")
		err = errdefs.WithSource(err, s.Source.Build._sourceMap.GetErrdefsSource())
		errs = append(errs, err)
	}

	if err := s.Source.validate(); err != nil {
		errs = append(errs, fmt.Errorf("build source: %w", err))
	}

	if len(errs) == 0 {
		return nil
	}
	return goerrors.Join(errs...)
}

func (src *SourceBuild) baseState(opts fetchOptions) llb.State {
	var name string

	if !src.Source.IsDir() {
		name = dockerui.DefaultDockerfileName
		if src.DockerfilePath != "" {
			name = src.DockerfilePath
		}
	}

	st := src.Source.ToState(name, opts.SourceOpt, opts.Constraints...)

	return st.Async(func(ctx context.Context, in llb.State, c *llb.Constraints) (llb.State, error) {
		// prepend the constraints passed into the async call to the ones from the source
		cOpts := []llb.ConstraintsOpt{WithConstraint(c)}
		cOpts = append(cOpts, opts.Constraints...)
		cOpts = append(cOpts, src._sourceMap.GetLocation(st))
		return opts.SourceOpt.Forward(in, src, cOpts...)
	})
}

func (src *SourceBuild) IsDir() bool {
	return true
}

func (src *SourceBuild) toState(opts fetchOptions) llb.State {
	return src.baseState(opts).With(sourceFilters(opts))
}

func (src *SourceBuild) toMount(opts fetchOptions) (llb.State, []llb.MountOption) {
	st := src.baseState(opts).With(mountFilters(opts))
	return st, nil
}

func (src *SourceBuild) fillDefaults(_ []*SourceGenerator) {
	bsrc := &src.Source
	bsrc.fillDefaults()
	src.Source = *bsrc
}

func (src *SourceBuild) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(key string) bool) error {
	var errs []error

	err := src.Source.processBuildArgs(lex, args, allowArg)
	if err != nil {
		errs = append(errs, fmt.Errorf("source: %w", err))
	}

	updated, err := expandArgs(lex, src.DockerfilePath, args, allowArg)
	if err != nil {
		errs = append(errs, fmt.Errorf("dockerfile path: %w", err))
	}
	src.DockerfilePath = updated

	updated, err = expandArgs(lex, src.Target, args, allowArg)
	if err != nil {
		errs = append(errs, fmt.Errorf("target: %w", err))
	}
	src.Target = updated

	if len(errs) > 0 {
		return fmt.Errorf("failed to expand args on build source: %w", goerrors.Join(errs...))
	}
	return nil
}

func (src *SourceBuild) doc(w io.Writer, name string) {
	printDocLn(w, "Generated from a docker build:")
	printDocLn(w, "	Docker Build Target:", src.Target)

	src.Source.toInterface().doc(&indentWriter{w}, name)

	if len(src.Args) > 0 {
		sorted := SortMapKeys(src.Args)
		for _, k := range sorted {
			printDocf(w, "		%s=%s\n", k, src.Args[k])
		}
	}

	p := "Dockerfile"
	if src.DockerfilePath != "" {
		p = src.DockerfilePath
	}
	printDocLn(w, "	Dockerfile path in context:", p)
}
