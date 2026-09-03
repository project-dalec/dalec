package dalec

import (
	"path/filepath"

	"github.com/moby/buildkit/client/llb"
)

const (
	BuildArgDalecSourceFilterConfigPath  = "DALEC_SOURCE_FILTER_CONFIG_PATH"
	BuildArgDalecSourceFilterContextName = "DALEC_SOURCE_FILTER_CONFIG_CONTEXT_NAME"
	DefaultSourceOptionsContextName      = "dalec-source-options"

	// DefaultSourceFilterConfigPath is the path, relative to the source options
	// build context, that the source filter config is read from when
	// [BuildArgDalecSourceFilterConfigPath] is not set.
	DefaultSourceFilterConfigPath = "source-filter.yml"
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
	return sourceFilterAtPath(sOpt, "", opts...)
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

		if !isRoot(base) {
			// prepend the base path to each excluded path
			joined := make([]string, 0, len(excludes))
			for _, path := range excludes {
				newPath := filepath.ToSlash(filepath.Join(base, path))
				joined = append(joined, newPath)
			}
			excludes = joined
		}

		return llb.Scratch().File(
			llb.Copy(in, "/", "/", WithDirContentsOnly(), WithExcludes(excludes)),
			opts...,
		)
	}
}
