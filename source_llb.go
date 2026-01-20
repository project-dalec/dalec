package dalec

import (
	"io"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
)

// SourceLLB is an internal-only source type that wraps an LLB state.
// It is not exposed to users via YAML/JSON marshaling (note the tags with "-").
// This is used for programmatically generated sources like gomod patches.
type SourceLLB struct {
	state      llb.State
	_sourceMap *sourceMap
}

// newSourceLLB creates an internal SourceLLB from an LLB state.
// This is not exposed to users and is used for programmatically generated sources.
func newSourceLLB(st llb.State) *SourceLLB {
	return &SourceLLB{
		state: st,
	}
}

func (s *SourceLLB) IsDir() bool {
	return false
}

func (s *SourceLLB) doc(io.Writer, string) {}

func (s *SourceLLB) fillDefaults([]*SourceGenerator) {}

func (s *SourceLLB) validate(fetchOptions) error { return nil }

func (s *SourceLLB) processBuildArgs(*shell.Lex, map[string]string, func(string) bool) error {
	return nil
}

func (s *SourceLLB) toMount(opt fetchOptions) (llb.State, []llb.MountOption) {
	st := s.toState(opt)
	if opt.Rename != "" {
		return st, []llb.MountOption{llb.SourcePath(opt.Rename)}
	}
	return st, nil
}

func (s *SourceLLB) toState(opt fetchOptions) llb.State {
	if opt.Rename != "" && opt.Rename != "/" {
		return llb.Scratch().File(llb.Copy(s.state, "/patch", "/"+opt.Rename, opt), opt.Constraints...)
	}
	return s.state
}
