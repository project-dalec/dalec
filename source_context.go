package dalec

import (
	"context"
	"io"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
	"github.com/moby/buildkit/solver/errdefs"
	"github.com/pkg/errors"
)

// SourceContext is used to generate a source from a build context. The path to
// the build context is provided to the `Path` field of the owning `Source`.
type SourceContext struct {
	// Name is the name of the build context. By default, it is the magic name
	// `context`, recognized by Docker as the default context.
	Name string `yaml:"name,omitempty" json:"name,omitempty"`

	_sourceMap *sourceMap `yaml:"-" json:"-"`
}

func (src *SourceContext) validate(opts fetchOptions) error {
	return nil
}

func (src *SourceContext) IsDir() bool {
	return true
}

func (src *SourceContext) baseState(opts fetchOptions) llb.State {
	return llb.Scratch().Async(func(ctx context.Context, _ llb.State, _ *llb.Constraints) (llb.State, error) {
		if opts.SourceOpt.GetContext == nil {
			panic("SourceOpt.GetContext is not set, cannot fetch source context")
		}
		st, err := opts.SourceOpt.GetContext(src.Name, opts)
		if err != nil {
			err = errdefs.WithSource(err, src._sourceMap.GetErrdefsSource())
			return llb.Scratch(), err
		}

		if st == nil {
			err := errors.Errorf("context %q not found", src.Name)
			err = errdefs.WithSource(err, src._sourceMap.GetErrdefsSource())
			return llb.Scratch(), err
		}
		return *st, nil
	})
}

func (src *SourceContext) toState(opts fetchOptions) llb.State {
	return src.baseState(opts).With(sourceFilters(opts))
}

func (src *SourceContext) toMount(opts fetchOptions) (llb.State, []llb.MountOption) {
	st := src.baseState(opts).With(mountFilters(opts))
	return st, nil
}

func (src *SourceContext) fillDefaults(_ []*SourceGenerator) {
	if src.Name == "" {
		src.Name = "context"
	}
}

func (src *SourceContext) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(key string) bool) error {
	updated, err := expandArgs(lex, src.Name, args, allowArg)
	if err != nil {
		return errors.Wrapf(err, "could not expand context name %q", src.Name)
	}
	src.Name = updated
	return nil
}

func (src *SourceContext) doc(w io.Writer, _ string) {
	printDocLn(w, "Generated from a local docker build context and is unreproducible.")
}
