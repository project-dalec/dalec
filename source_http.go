package dalec

import (
	stderrors "errors"
	"io"
	"io/fs"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
	"github.com/moby/buildkit/solver/errdefs"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
)

// SourceHTTP is used to download a file from an HTTP(s) URL.
type SourceHTTP struct {
	// URL is the URL to download the file from.
	URL string `yaml:"url" json:"url"`
	// Digest is the digest of the file to download.
	// This is used to verify the integrity of the file.
	// Form: <algorithm>:<digest>
	Digest digest.Digest `yaml:"digest,omitempty" json:"digest,omitempty"`
	// Permissions is the octal file permissions to set on the file.
	Permissions fs.FileMode `yaml:"permissions,omitempty" json:"permissions,omitempty"`

	_sourceMap *sourceMap `yaml:"-" json:"-"`
}

func (src *SourceHTTP) validate(opts fetchOptions) error {
	var errs []error

	if src.URL == "" {
		errs = append(errs, errors.New("http source must have a URL"))
	}
	if src.Digest != "" {
		if err := src.Digest.Validate(); err != nil {
			errs = append(errs, err)
		}
	}

	if len(opts.Excludes) > 0 {
		errs = append(errs, errors.New("http source cannot be used with excludes"))
	}
	if len(opts.Includes) > 0 {
		errs = append(errs, errors.New("http source cannot be used with includes"))
	}
	if opts.Path != "" {
		errs = append(errs, errors.New("http source cannot be used with path"))
	}

	if len(errs) > 0 {
		err := stderrors.Join(errs...)
		err = errdefs.WithSource(err, src._sourceMap.GetErrdefsSource())
		return err
	}

	return nil
}

func (src *SourceHTTP) toState(opts fetchOptions) llb.State {
	var httpOpts []llb.HTTPOption

	httpOpts = append(httpOpts, WithConstraints(opts.Constraints...))
	if src.Digest != "" {
		httpOpts = append(httpOpts, llb.Checksum(src.Digest))
	}

	if src.Permissions != 0 {
		httpOpts = append(httpOpts, llb.Chmod(src.Permissions))
	}

	if opts.Rename != "" {
		httpOpts = append(httpOpts, llb.Filename(opts.Rename))
	}
	httpOpts = append(httpOpts, src._sourceMap.GetRootLocation())
	return llb.HTTP(src.URL, httpOpts...)
}

func (src *SourceHTTP) IsDir() bool {
	return false
}

func (src *SourceHTTP) toMount(opts fetchOptions) (llb.State, []llb.MountOption) {
	if isRoot(opts.Rename) {
		opts.Rename = internalMountSourceName
	}

	st := src.toState(opts)
	// This is always a file, so to make sure we always mount a file instead of
	// a directory we need to add a mount opt pointing at the file name.
	mountOpts := []llb.MountOption{llb.SourcePath(opts.Rename)}
	return st, mountOpts
}

func (src *SourceHTTP) fillDefaults(_ []*SourceGenerator) {}

func (src *SourceHTTP) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(key string) bool) error {
	updated, err := expandArgs(lex, src.URL, args, allowArg)
	if err != nil {
		err := errors.Wrap(err, "failed to expand HTTP URL")
		return errdefs.WithSource(err, src._sourceMap.GetErrdefsSource())
	}
	src.URL = updated
	return nil
}

func (src *SourceHTTP) doc(w io.Writer, name string) {
	printDocLn(w, "Generated from a http(s) source:")
	printDocLn(w, "	URL:", src.URL)
}
