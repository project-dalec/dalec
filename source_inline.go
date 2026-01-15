package dalec

import (
	"context"
	goerrors "errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml/ast"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
	"github.com/moby/buildkit/solver/errdefs"
	"github.com/pkg/errors"
)

const (
	// This is used as the source name for sources in specified in `SourceMount`
	// For any sources we need to mount we need to give the source a name.
	// We don't actually care about the name here *except* the way file-backed
	// sources work the name of the file becomes the source name.
	// So we at least need to track it.
	// Source names must also not contain path separators or it can screw up the logic.
	//
	// To note, the name of the source affects how the source is cached, so this
	// should just be a single specific name so we can get maximal cache re-use.
	internalMountSourceName = "mount"
)

// SourceInlineFile is used to specify the content of an inline source.
type SourceInlineFile struct {
	// Contents is the content.
	Contents string `yaml:"contents,omitempty" json:"contents,omitempty"`
	// Permissions is the octal file permissions to set on the file.
	Permissions fs.FileMode `yaml:"permissions,omitempty" json:"permissions,omitempty"`
	// UID is the user ID to set on the directory and all files and directories within it.
	// UID must be greater than or equal to 0
	UID int `yaml:"uid,omitempty" json:"uid,omitempty"`
	// GID is the group ID to set on the directory and all files and directories within it.
	// UID must be greater than or equal to 0
	GID int `yaml:"gid,omitempty" json:"gid,omitempty"`

	_sourceMap    *sourceMap
	_uidSourceMap *sourceMap
	_gidSourceMap *sourceMap
}

func (s *SourceInlineFile) UnmarshalYAML(ctx context.Context, node ast.Node) error {
	type internal struct {
		Contents    string                 `yaml:"contents,omitempty" json:"contents,omitempty"`
		Permissions fs.FileMode            `yaml:"permissions,omitempty" json:"permissions,omitempty"`
		UID         sourceMappedValue[int] `yaml:"uid,omitempty" json:"uid"`
		GID         sourceMappedValue[int] `yaml:"gid,omitempty" json:"gid"`
	}
	var i internal

	dec := getDecoder(ctx)
	if err := dec.DecodeFromNodeContext(ctx, node, &i); err != nil {
		return errors.Wrap(err, "failed to decode inline file")
	}

	*s = SourceInlineFile{
		Contents:    i.Contents,
		Permissions: i.Permissions,
		UID:         i.UID.Value,
		GID:         i.GID.Value,
	}
	s._sourceMap = newSourceMap(ctx, node)
	s._uidSourceMap = i.UID.sourceMap
	s._gidSourceMap = i.GID.sourceMap
	return nil
}

// SourceInlineDir is used by by [SourceInline] to represent a filesystem directory.
type SourceInlineDir struct {
	// Files is the list of files to include in the directory.
	// The map key is the name of the file.
	//
	// Files with path separators in the key will be rejected.
	Files map[string]*SourceInlineFile `yaml:"files,omitempty" json:"files,omitempty"`
	// Permissions is the octal permissions to set on the directory.
	Permissions fs.FileMode `yaml:"permissions,omitempty" json:"permissions,omitempty"`

	// UID is the user ID to set on the directory and all files and directories within it.
	// UID must be greater than or equal to 0
	UID int `yaml:"uid,omitempty" json:"uid,omitempty"`
	// GID is the group ID to set on the directory and all files and directories within it.
	// UID must be greater than or equal to 0
	GID int `yaml:"gid,omitempty" json:"gid,omitempty"`

	_sourceMap    *sourceMap
	_gidSourceMap *sourceMap
	_uidSourceMap *sourceMap
}

func (s *SourceInlineDir) UnmarshalYAML(ctx context.Context, node ast.Node) error {
	type internal struct {
		Files       map[string]*SourceInlineFile `yaml:"files,omitempty" json:"files"`
		Permissions fs.FileMode                  `yaml:"permissions,omitempty" json:"permissions"`
		UID         sourceMappedValue[int]       `yaml:"uid,omitempty" json:"uid"`
		GID         sourceMappedValue[int]       `yaml:"gid,omitempty" json:"gid"`
	}
	var i internal

	dec := getDecoder(ctx)
	if err := dec.DecodeFromNodeContext(ctx, node, &i); err != nil {
		return errors.Wrap(err, "failed to decode inline dir")
	}
	*s = SourceInlineDir{
		Files:       i.Files,
		Permissions: i.Permissions,
		UID:         i.UID.Value,
		GID:         i.GID.Value,
	}
	s._sourceMap = newSourceMap(ctx, node)
	s._uidSourceMap = i.UID.sourceMap
	s._gidSourceMap = i.GID.sourceMap
	return nil
}

// SourceInline is used to generate a source from inline content.
type SourceInline struct {
	// File is the inline file to generate.
	// File is treated as a literal single file.
	// [SourceIsDir] will return false when this is set.
	// This is mutually exclusive with [Dir]
	File *SourceInlineFile `yaml:"file,omitempty" json:"file,omitempty"`
	// Dir creates a directory with the given files and directories.
	// [SourceIsDir] will return true when this is set.
	// This is mutually exclusive with [File]
	Dir *SourceInlineDir `yaml:"dir,omitempty" json:"dir,omitempty"`

	_sourceMap *sourceMap
}

func (src *SourceInline) IsDir() bool {
	return src.Dir != nil
}

const (
	defaultFilePerms = 0o644
	defaultDirPerms  = 0o755
)

func (s *SourceInline) validate(opts fetchOptions) (retErr error) {
	var errs []error

	if s.File == nil && s.Dir == nil {
		err := errors.New("inline source is missing contents to inline")
		err = errdefs.WithSource(err, s._sourceMap.GetErrdefsSource())
		errs = append(errs, err)
	}

	if s.File != nil && s.Dir != nil {
		err := errors.New("inline source variant cannot have both a file and dir set")
		err = errdefs.WithSource(err, s._sourceMap.GetErrdefsSource())
		errs = append(errs, err)
	}

	if s.Dir != nil {
		if err := s.Dir.validate(); err != nil {
			errs = append(errs, err)
		}
	}

	if s.File != nil {
		if opts.Path != "" {
			errs = append(errs, errors.New("inline file source cannot have a path set"))
		}
		if opts.Includes != nil {
			errs = append(errs, errors.New("inline file source cannot have includes set"))
		}
		if opts.Excludes != nil {
			errs = append(errs, errors.New("inline file source cannot have excludes set"))
		}
		if err := s.File.validate(); err != nil {
			errs = append(errs, errors.Wrap(err, "inline file source validation failed"))
		}
	}

	return goerrors.Join(errs...)
}

func (s *SourceInlineDir) validate() error {
	var errs []error

	if s.UID < 0 {
		err := errors.Errorf("uid %d must be non-negative", s.UID)
		err = errdefs.WithSource(err, s._uidSourceMap.GetErrdefsSource())
		errs = append(errs, err)
	}

	if s.GID < 0 {
		err := errors.Errorf("gid %d must be non-negative", s.GID)
		err = errdefs.WithSource(err, s._gidSourceMap.GetErrdefsSource())
		errs = append(errs, err)
	}

	for k, f := range s.Files {
		if strings.ContainsRune(k, os.PathSeparator) {
			errs = append(errs, errors.Wrapf(errSourceNamePathSeparator, "file %q", k))
		}
		if err := f.validate(); err != nil {
			errs = append(errs, errors.Wrapf(err, "file %q", k))
		}
	}

	return goerrors.Join(errs...)
}

func (s *SourceInlineFile) validate() error {
	var errs []error

	if s.UID < 0 {
		err := errors.Errorf("uid %d must be non-negative", s.UID)
		err = errdefs.WithSource(err, s._uidSourceMap.GetErrdefsSource())
		errs = append(errs, err)
	}

	if s.GID < 0 {
		err := errors.Errorf("gid %d must be non-negative", s.GID)
		err = errdefs.WithSource(err, s._gidSourceMap.GetErrdefsSource())
		errs = append(errs, err)
	}

	return goerrors.Join(errs...)
}

func (s *SourceInline) Doc(w io.Writer, name string) {
	if s.File != nil {
		s.File.Doc(w, name)
	}

	if s.Dir != nil {
		s.Dir.Doc(w, name)
	}
}

// Doc writes the information about the file to the writer.
func (s *SourceInlineFile) Doc(w io.Writer, name string) {
	printDocLn(w, `	cat << EOF > `+name+`
`+s.Contents+`
	EOF`)

	if s.UID != 0 {
		printDocLn(w, `	chown `+strconv.Itoa(s.UID)+" "+name)
	}
	if s.GID != 0 {
		printDocLn(w, `	chgrp `+strconv.Itoa(s.GID)+" "+name)
	}

	perms := s.Permissions.Perm()
	if perms == 0 {
		perms = 0o644
	}
	printDocf(w, "	chmod %o %s\n", perms.Perm(), name)
}

// Doc writes the information about the directory to the writer.
func (s *SourceInlineDir) Doc(w io.Writer, name string) {
	printDocLn(w, `	mkdir -p `+name)

	if s.UID != 0 {
		printDocLn(w, `	chown `+strconv.Itoa(s.UID)+" "+name)
	}
	if s.GID != 0 {
		printDocLn(w, `	chgrp `+strconv.Itoa(s.GID)+" "+name)
	}

	perms := s.Permissions.Perm()
	if perms == 0 {
		perms = 0o644
	}

	sorted := SortMapKeys(s.Files)
	for _, k := range sorted {
		v := s.Files[k]
		v.Doc(w, filepath.Join(name, k))
	}

	printDocf(w, "	chmod %o %s\n", perms.Perm(), name)
}

func (s *SourceInline) toState(opts fetchOptions) llb.State {
	if s.File != nil {
		return llb.Scratch().With(s.File.toState(opts))
	}
	return s.Dir.toState(opts)
}

func (s *SourceInline) toMount(opts fetchOptions) (llb.State, []llb.MountOption) {
	if s.File != nil {
		return s.File.toMount(opts)
	}
	return s.Dir.toMount(opts)
}

func (s *SourceInlineFile) toState(opts fetchOptions) llb.StateOption {
	return func(in llb.State) llb.State {
		if isRoot(opts.Rename) {
			// If we are here, then something is very much not right and is almost
			// certainly a dalec bug.
			panic(fmt.Sprintf("invalid file name: %q", opts.Rename))
		}
		st := in.File(llb.Mkfile(opts.Rename, s.Permissions, []byte(s.Contents), llb.WithUIDGID(int(s.UID), int(s.GID))), opts.Constraints...)
		return st
	}
}

func (s *SourceInlineFile) toMount(opts fetchOptions) (llb.State, []llb.MountOption) {
	if isRoot(opts.Rename) {
		opts.Rename = internalMountSourceName
	}

	// This is always a file, so to make sure we always mount a file instead of
	// a directory we need to add a mount opt pointing at the file name.
	mountOpts := []llb.MountOption{llb.SourcePath(opts.Rename)}

	st := llb.Scratch().With(s.toState(opts))
	return st, mountOpts
}

func (s *SourceInlineDir) toState(opts fetchOptions) llb.State {
	base := s.baseState(opts)
	// inline dir handles dir names and subpaths itself
	// Do not pass rename to sourceFilters
	opts.Rename = ""
	return base.With(sourceFilters(opts))
}

func (s *SourceInlineDir) baseState(opts fetchOptions) llb.State {
	st := llb.Scratch().File(llb.Mkdir(opts.Rename, s.Permissions, llb.WithUIDGID(int(s.UID), int(s.GID))), opts.Constraints...)
	sorted := SortMapKeys(s.Files)
	for _, k := range sorted {
		if !isRoot(opts.Path) && opts.Path != k {
			continue
		}
		f := s.Files[k]
		opts := opts
		opts.Rename = filepath.Join(opts.Rename, k)
		st = st.With(f.toState(opts))
	}
	return st
}

func (s *SourceInlineDir) toMount(opts fetchOptions) (llb.State, []llb.MountOption) {
	st := s.toState(opts).With(mountFilters(opts))
	return st, nil
}

func (s *SourceInlineFile) fillDefaults(_ []*SourceGenerator) {
	if s.Permissions == 0 {
		s.Permissions = defaultFilePerms
	}
}

func (s *SourceInlineDir) fillDefaults(gen []*SourceGenerator) {
	if s.Permissions == 0 {
		s.Permissions = defaultDirPerms
	}

	for _, f := range s.Files {
		f.fillDefaults(gen)
	}
}

func (s *SourceInline) fillDefaults(gen []*SourceGenerator) {
	if s.File != nil {
		s.File.fillDefaults(gen)
	}

	if s.Dir != nil {
		s.Dir.fillDefaults(gen)
	}
}

func (s *SourceInline) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(key string) bool) error {
	return nil
}

func (src *SourceInline) doc(w io.Writer, name string) {
	printDocLn(w, "Generated from an inline source:")
	src.Doc(w, name)
}
