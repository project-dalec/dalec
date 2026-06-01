package dalec

import (
	"path/filepath"

	"github.com/moby/buildkit/client/llb"
)

const (
	BuildArgDalecSourceFilterConfigPath  = "DALEC_SOURCE_FILTER_CONFIG_PATH"
	BuildArgDalecSourceFilterContextName = "DALEC_SOURCE_FILTER_CONFIG_CONTEXT_NAME"
	DefaultSourceOptionsContextName      = "dalec-source-options"
)

// SourceFilterConfig configures build-time filtering for source package inputs.
// It is intentionally global; future versions may add more specific filter
// scopes alongside GlobalExcludes.
type SourceFilterConfig struct {
	GlobalExcludes []string `yaml:"global_excludes,omitempty" json:"global_excludes,omitempty"`
}

func (sOpt SourceOpts) GetSourceFilter() (SourceFilterConfig, error) {
	if sOpt.SourceFilter == nil {
		return SourceFilterConfig{}, nil
	}
	return sOpt.SourceFilter()
}

func (cfg SourceFilterConfig) IsEmpty() bool {
	return len(cfg.GlobalExcludes) == 0
}

func (sOpt SourceOpts) sourceFilterExcludes() ([]string, error) {
	cfg, err := sOpt.GetSourceFilter()
	if err != nil {
		return nil, err
	}
	return cfg.GlobalExcludes, nil
}

func sourceFilter(sOpt SourceOpts, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		excludes, err := sOpt.sourceFilterExcludes()
		if err != nil {
			return ErrorState(in, err)
		}
		if len(excludes) == 0 {
			return in
		}
		return in.With(SourceFilter(SourceFilterConfig{GlobalExcludes: excludes}, opts...))
	}
}

func sourceFilterAtPath(sOpt SourceOpts, base string, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		excludes, err := sOpt.sourceFilterExcludes()
		if err != nil {
			return ErrorState(in, err)
		}
		if len(excludes) == 0 {
			return in
		}
		return in.With(SourceFilterAtPath(base, SourceFilterConfig{GlobalExcludes: excludes}, opts...))
	}
}

// SourceFilter filters source package content from the root of the input state.
func SourceFilter(cfg SourceFilterConfig, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		if cfg.IsEmpty() {
			return in
		}
		return llb.Scratch().File(llb.Copy(in, "/", "/", WithDirContentsOnly(), WithExcludes(cfg.GlobalExcludes)), opts...)
	}
}

// SourceFilterAtPath applies a global source filter to content nested under base.
// The external config remains global while named source states keep their source
// name as a top-level directory in package source assembly.
func SourceFilterAtPath(base string, cfg SourceFilterConfig, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		if cfg.IsEmpty() {
			return in
		}
		if isRoot(base) {
			return in.With(SourceFilter(cfg, opts...))
		}

		excludes := make([]string, 0, len(cfg.GlobalExcludes))
		for _, exclude := range cfg.GlobalExcludes {
			excludes = append(excludes, filepath.ToSlash(filepath.Join(base, exclude)))
		}

		return SourceFilter(SourceFilterConfig{GlobalExcludes: excludes}, opts...)(in)
	}
}
