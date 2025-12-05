//go:generate go run ./cmd/gen-source-variants source_generated.go
package dalec

import (
	"bytes"
	"context"
	"encoding/json"
	goerrors "errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml/ast"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/solver/errdefs"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

type FilterFunc = func(string, []string, []string, ...llb.ConstraintsOpt) llb.StateOption

var errNoSourceVariant = fmt.Errorf("no source variant found")

// Source defines a source to be used in the build.
// A source can be a local directory, a git repositoryt, http(s) URL, etc.
type Source struct {
	// This is an embedded union representing all of the possible source types.
	// Exactly one must be non-nil, with all other cases being errors.
	//
	// === Begin Source Variants ===
	DockerImage *SourceDockerImage `yaml:"image,omitempty" json:"image,omitempty"`
	Git         *SourceGit         `yaml:"git,omitempty" json:"git,omitempty"`
	HTTP        *SourceHTTP        `yaml:"http,omitempty" json:"http,omitempty"`
	Context     *SourceContext     `yaml:"context,omitempty" json:"context,omitempty"`
	Build       *SourceBuild       `yaml:"build,omitempty" json:"build,omitempty"`
	Inline      *SourceInline      `yaml:"inline,omitempty" json:"inline,omitempty"`
	LLB         *SourceLLB         `yaml:"-" json:"-"` // Internal only, not exposed to users
	// === End Source Variants ===

	// Path is the path to the source after fetching it based on the identifier.
	Path string `yaml:"path,omitempty" json:"path,omitempty"`

	// Includes is a list of paths underneath `Path` to include, everything else is execluded
	// If empty, everything is included (minus the excludes)
	Includes []string `yaml:"includes,omitempty" json:"includes,omitempty"`
	// Excludes is a list of paths underneath `Path` to exclude, everything else is included
	Excludes []string `yaml:"excludes,omitempty" json:"excludes,omitempty"`

	// Generate specifies a list of dependency generators to apply to a given source.
	//
	// Generators are used to generate additional sources from this source.
	// As an example the `gomod` generator can be used to generate a go module cache from a go source.
	// How a generator operates is dependent on the actual generator.
	// Generators may also cause modifications to the build environment.
	//
	// Currently supported generators are: "gomod", "cargohome", and "pip".
	// The "gomod" generator will generate a go module cache from the source.
	// The "cargohome" generator will generate a cargo home from the source.
	// The "pip" generator will generate a pip cache from the source.
	Generate []*SourceGenerator `yaml:"generate,omitempty" json:"generate,omitempty"`

	_sourceMap *sourceMap `json:"-" yaml:"-"`
}

func (s *Source) UnmarshalYAML(ctx context.Context, node ast.Node) error {
	type internal Source
	var i internal

	dec := getDecoder(ctx)
	if err := dec.DecodeFromNodeContext(ctx, node, &i); err != nil {
		return errors.Wrap(err, "failed to decode source")
	}

	*s = Source(i)
	s._sourceMap = newSourceMap(ctx, node)
	return nil
}

// withFollowPath similar to using [llb.IncludePatterns] except that it will
// follow symlinks at the provided path.
func withFollowPath(p string) localOptionFunc {
	return func(li *llb.LocalInfo) {
		if isRoot(p) {
			return
		}

		paths := []string{p}
		if li.FollowPaths != "" {
			var ls []string
			if err := json.Unmarshal([]byte(li.FollowPaths), &ls); err != nil {
				panic(err)
			}
			paths = append(ls, paths...)
		}
		llb.FollowPaths(paths).SetLocalOption(li)
	}
}

func excludeAllButPath(p string) []string {
	return []string{
		"*",
		"!" + filepath.ToSlash(filepath.Clean(p)),
	}
}

// InvalidSourceError is an error type returned when a source is invalid.
type InvalidSourceError struct {
	Name string
	Err  error
}

func (s *InvalidSourceError) Error() string {
	return fmt.Sprintf("invalid source %s: %v", s.Name, s.Err)
}

func (s *InvalidSourceError) Unwrap() error {
	return s.Err
}

type InvalidPatchError struct {
	Source    string
	PatchSpec *PatchSpec
	Err       error
}

func (s *InvalidPatchError) Error() string {
	return fmt.Sprintf("invalid patch for source %q, patch source: %q: %v", s.Source, s.PatchSpec.Source, s.Err)
}

func (s *InvalidPatchError) Unwrap() error {
	return s.Err
}

var (
	errSourceNamePathSeparator = goerrors.New("source name must not contain path separator")
	errMissingSource           = goerrors.New("source is missing from sources list")

	errPatchRequiresSubpath = goerrors.New("patch source refers to a directory source without a subpath to the patch file to use")
	errPatchFileNoSubpath   = goerrors.New("patch source refers to a file source but patch spec specifies a subpath")
)

type LLBGetter func(sOpts SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, error)

type ForwarderFunc func(llb.State, *SourceBuild, ...llb.ConstraintsOpt) (llb.State, error)

type SourceOpts struct {
	Resolver         llb.ImageMetaResolver
	Forward          ForwarderFunc
	GetContext       func(string, ...llb.LocalOption) (*llb.State, error)
	TargetPlatform   *ocispecs.Platform
	GitCredHelperOpt func() (llb.RunOption, error)
}

var errInvalidMountConfig = errors.New("invalid mount config")

func pathHasPrefix(s string, prefix string) bool {
	if s == prefix {
		return true
	}

	s = filepath.Clean(s)
	prefix = filepath.Clean(prefix)

	if s == prefix {
		return true
	}

	if strings.HasPrefix(s, prefix+"/") {
		return true
	}
	return false
}

func isRoot(extract string) bool {
	return extract == "" || extract == "/" || extract == "."
}

func WithCreateDestPath() llb.CopyOption {
	return copyOptionFunc(func(i *llb.CopyInfo) {
		i.CreateDestPath = true
	})
}

func SourceIsDir(src Source) bool {
	return src.IsDir()
}

// Doc returns the details of how the source was created.
// This should be included, where applicable, in build in build specs (such as RPM spec files)
// so that others can reproduce the build.
func (s Source) Doc(name string) io.Reader {
	buf := bytes.NewBuffer(nil)
	s.toInterface().doc(buf, name)
	if s.Path != "" {
		fmt.Fprintln(buf, "	Extracted path:", s.Path)
	}
	return buf
}

func patchSource(worker, sourceState llb.State, sourceToState map[string]llb.State, patchNames []PatchSpec, subPath string, sources map[string]Source, sourceName string, opts ...llb.ConstraintsOpt) llb.State {
	for _, p := range patchNames {
		patchState := sourceToState[p.Source]
		// on each iteration, mount source state to /src to run `patch`, and
		// set the state under /src to be the source state for the next iteration

		patchPath := filepath.Join(p.Source, p.Path)

		cmd := fmt.Sprintf("patch -p%d < /patch", *p.Strip)
		sourceState = worker.Run(
			llb.AddMount("/patch", patchState, llb.Readonly, llb.SourcePath(patchPath)),
			llb.Dir(filepath.Join("src", subPath)),
			ShArgs(cmd),
			llb.WithCustomNamef("Apply patch %q to source %q: %s", p.Source, sourceName, cmd),
			p._sourceMap.GetLocation(sourceState),                   // patch spec
			sources[p.Source]._sourceMap.GetLocation(patchState),    // patch source
			sources[sourceName]._sourceMap.GetLocation(sourceState), // patch target
			WithConstraints(opts...),
		).AddMount("/src", sourceState)
	}

	return sourceState
}

// PatchSources returns a new map containing the patched LLB state for each source in the source map.
// Sources that are not patched are also included in the result for convenience.
// `sourceToState` must be a complete map from source name -> llb state for each source in the dalec spec.
// `worker` must be an LLB state with a `patch` binary present.
func PatchSources(worker llb.State, spec *Spec, sourceToState map[string]llb.State, opts ...llb.ConstraintsOpt) map[string]llb.State {
	// duplicate map to avoid possibly confusing behavior of mutating caller's map
	states := DuplicateMap(sourceToState)
	pgID := identity.NewID()
	sorted := SortMapKeys(states)

	for _, sourceName := range sorted {
		sourceState := states[sourceName]

		patches, patchesExist := spec.Patches[sourceName]
		if !patchesExist {
			continue
		}
		pg := llb.ProgressGroup(pgID, "Patch spec source: "+sourceName+" ", false)
		states[sourceName] = patchSource(worker, sourceState, states, patches, sourceName, spec.Sources, sourceName, pg, withConstraints(opts))
	}

	return states
}

func (s *Spec) getPatchedSources(sOpt SourceOpts, worker llb.State, filterFunc func(string) bool, opts ...llb.ConstraintsOpt) map[string]llb.State {
	states := map[string]llb.State{}
	for name, src := range s.Sources {
		if !filterFunc(name) {
			continue
		}

		st := src.ToState(name, sOpt, opts...)
		states[name] = st
		for _, p := range s.Patches[name] {
			src, ok := s.Sources[p.Source]
			if !ok {
				states[p.Source] = ErrorState(llb.Scratch(), errors.Errorf("patch source %q not found", p.Source))
				continue
			}
			states[p.Source] = src.ToState(p.Source, sOpt, opts...)
		}
	}

	return PatchSources(worker, s, states, opts...)
}

// Tar creates a tar+gz from the provided state and puts it in the provided dest.
// The provided work state is used to perform the necessary operations to produce the tarball and requires the tar and gzip binaries.
func Tar(work llb.State, st llb.State, dest string, opts ...llb.ConstraintsOpt) llb.State {

	// Put the output tar in a consistent location regardless of `dest`
	// This way if `dest` changes we don't have to rebuild the tarball, which can be expensive.
	outBase := "/tmp/out"
	out := filepath.Join(outBase, filepath.Dir(dest))
	worker := work.Run(
		llb.AddMount("/src", st, llb.Readonly),
		ShArgs("tar -C /src -czf /tmp/st ."),
		WithConstraints(opts...),
	).
		Run(
			llb.Args([]string{"/bin/sh", "-c", "mkdir -p " + out + " && mv /tmp/st " + filepath.Join(out, filepath.Base(dest))}),
			WithConstraints(opts...),
		)

	return worker.AddMount(outBase, llb.Scratch())
}

// AsTar returns an [llb.StateOption] which converts the input state into a tar
// with the given "dest" path as the name.
func AsTar(worker llb.State, dest string, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		return Tar(worker, in, dest, opts...)
	}
}

func DefaultTarWorker(resolver llb.ImageMetaResolver, opts ...llb.ConstraintsOpt) llb.State {
	return llb.Image("busybox:latest", llb.WithMetaResolver(resolver), withConstraints(opts))
}

// Sources gets all the source LLB states from the spec.
func Sources(spec *Spec, sOpt SourceOpts, opts ...llb.ConstraintsOpt) map[string]llb.State {
	states := make(map[string]llb.State, len(spec.Sources))

	for k, src := range spec.Sources {
		pg := ProgressGroup("Prepare source: " + k)
		opts := append(opts, pg)

		st := src.ToState(k, sOpt, opts...)
		states[k] = st
	}
	return states
}

func (g *SourceGenerator) fillDefaults(host string, authInfo *GitAuth) {
	if g == nil || authInfo == nil {
		return
	}

	switch {
	case g.Gomod != nil:
		g.Gomod.fillDefaults(host, authInfo)
	}
}

func (gm *GeneratorGomod) fillDefaults(host string, authInfo *GitAuth) {
	// Don't overwrite explicitly-specified auth
	_, ok := gm.Auth[host]
	if ok {
		return
	}
	const defaultUsername = "git"

	var gomodAuth GomodGitAuth
	switch {
	case authInfo.Token != "":
		gomodAuth.Token = authInfo.Token
	case authInfo.Header != "":
		gomodAuth.Header = authInfo.Header
	case authInfo.SSH != "":
		gomodAuth.SSH = &GomodGitAuthSSH{
			ID:       authInfo.SSH,
			Username: defaultUsername,
		}
	default:
		return
	}

	if gm.Auth == nil {
		gm.Auth = make(map[string]GomodGitAuth)
	}

	gm.Auth[host] = gomodAuth
}

func (s *Source) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(key string) bool) error {
	// force the shell lexer to skip unresolved env vars so they aren't
	// replaced with ""
	lex.SkipUnsetEnv = true
	var errs []error
	appendErr := func(err error) {
		errs = append(errs, err)
	}

	if s.Path != "" {
		updated, err := expandArgs(lex, s.Path, args, allowArg)
		if err != nil {
			appendErr(err)
		} else {
			s.Path = updated
		}
	}

	for i, g := range s.Includes {
		updated, err := expandArgs(lex, g, args, allowArg)
		if err != nil {
			appendErr(err)
			continue
		}
		s.Includes[i] = updated
	}

	for i, g := range s.Excludes {
		updated, err := expandArgs(lex, g, args, allowArg)
		if err != nil {
			appendErr(err)
			continue
		}
		s.Excludes[i] = updated
	}

	if err := s.toInterface().processBuildArgs(lex, args, allowArg); err != nil {
		appendErr(err)
	}

	for _, gen := range s.Generate {
		if err := gen.processBuildArgs(args, allowArg); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.Wrapf(goerrors.Join(errs...), "failed to process build args for source")
	}
	return nil
}

func (g *SourceGenerator) processBuildArgs(args map[string]string, allowArg func(key string) bool) error {
	var errs []error

	if g.Gomod != nil {
		if err := g.Gomod.processBuildArgs(args, allowArg); err != nil {
			errs = append(errs, err)
		}
	}

	return goerrors.Join(errs...)
}

func (s *Source) validate() error {
	var errs []error

	for i, g := range s.Generate {
		if err := g.Validate(); err != nil {
			errs = append(errs, errors.Wrapf(err, "source generator %d", i))
		}
	}

	var invalid bool
	if err := s.validateSourceVariants(); err != nil {
		invalid = true
		err = errdefs.WithSource(err, s._sourceMap.GetErrdefsSource())
		errs = append(errs, err)
	}

	if !invalid {
		// Only validate the source if it is a valid source variant so as to avoid panics.
		if err := s.toInterface().validate(s.fetchOptions(SourceOpts{})); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return goerrors.Join(errs...)
	}

	return nil
}

type source interface {
	validate(fetchOptions) error
	fillDefaults([]*SourceGenerator)
	processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(key string) bool) error

	toState(fetchOptions) llb.State
	toMount(opt fetchOptions) (llb.State, []llb.MountOption)

	doc(w io.Writer, name string)
	IsDir() bool
}

type fetchOptions struct {
	Constraints []llb.ConstraintsOpt
	Path        string
	Includes    []string
	Excludes    []string
	Rename      string
	SourceOpt   SourceOpts
}

func (s *Source) fetchOptions(sOpt SourceOpts) fetchOptions {
	return fetchOptions{
		Includes:  s.Includes,
		Excludes:  s.Excludes,
		Path:      s.Path,
		SourceOpt: sOpt,
	}
}

func (s *Source) ToState(name string, sOpt SourceOpts, opts ...llb.ConstraintsOpt) llb.State {
	fo := s.fetchOptions(sOpt)
	fo.Constraints = opts
	fo.Rename = name
	st := s.toInterface().toState(fo)
	return st
}

func (s *Source) ToMount(sOpt SourceOpts, constraints ...llb.ConstraintsOpt) (llb.State, []llb.MountOption) {
	fo := s.fetchOptions(sOpt)
	fo.Constraints = append(fo.Constraints, constraints...)

	st, mountOpts := s.toInterface().toMount(fo)
	if !isRoot(s.Path) {
		// Prepend source path to mount opts so that the returned options can
		// overwrite that.
		mountOpts = append([]llb.MountOption{llb.SourcePath(s.Path)}, mountOpts...)
	}
	return st, mountOpts
}

func (s *Source) IsDir() bool {
	return s.toInterface().IsDir()
}

func sourceFilters(opts fetchOptions) llb.StateOption {
	return func(in llb.State) llb.State {
		if opts.Rename == "" && len(opts.Includes) == 0 && len(opts.Excludes) == 0 && isRoot(opts.Path) {
			return in
		}
		if opts.Path == "" {
			opts.Path = "/"
		}

		// Append the path separator (i.e. "/") to the end of opts.Rename to ensure
		// that if the apth being copied is a file that the file is put *into* `opts.Rename`
		// instead of being called `opts.Rename` itself.
		// Example:
		//	Given we have a file some/path/to/file
		//	When opts.Path is some/path/to/file and we want that to go into /other/file
		//	Then we need to set tell buildkit to copy to /other/ (not /other)
		//  Else buildkit will rename the file to /other
		sep := string(os.PathSeparator)
		rename := opts.Rename + string(sep)
		return llb.Scratch().File(llb.Copy(in, opts.Path, rename, opts), opts.Constraints...)
	}
}

// SetCopyOptions is an llb.CopyOption that sets the includes and excludes for a copy operation.
func (opts fetchOptions) SetCopyOption(info *llb.CopyInfo) {
	if len(opts.Includes) > 0 {
		WithIncludes(opts.Includes).SetCopyOption(info)
	}
	if len(opts.Excludes) > 0 {
		WithExcludes(opts.Excludes).SetCopyOption(info)
	}
	info.CopyDirContentsOnly = true
}

// SetLocalOption is an llb.LocalOption that sets various options needed for local (or context) backed sources.
func (opts fetchOptions) SetLocalOption(info *llb.LocalInfo) {
	includes := opts.Includes
	excludes := opts.Excludes
	isRoot := isRoot(opts.Path)

	// For all include/exclude patterns, we need to prepend the base path
	// since the dalec spec assumes that include/excludes are relative to the requested source path.
	// Since we are relying on the underlying LLB implementation to handle the filtering and not requesting
	// more data from the client than it should, we need to ensure that the paths are correct.

	if len(excludes) > 0 && !isRoot {
		excludes = make([]string, len(excludes))
		for i, exclude := range opts.Excludes {
			excludes[i] = filepath.Join(opts.Path, exclude)
		}
	}

	if len(opts.Includes) > 0 && !isRoot {
		includes = make([]string, len(opts.Includes))
		for i, include := range opts.Includes {
			includes[i] = filepath.Join(opts.Path, include)
		}
	}

	if !isRoot {
		// Exclude anything that is not underneath the requested path
		// This way we aren't needlessly copying data that is not needed.
		excludes = append(excludeAllButPath(opts.Path), excludes...)
	}

	localIncludeExcludeMerge(includes, excludes).SetLocalOption(info)
	if len(opts.Constraints) > 0 {
		WithConstraints(opts.Constraints...).SetLocalOption(info)
	}

	withFollowPath(opts.Path).SetLocalOption(info)
}

func mountFilters(opts fetchOptions) llb.StateOption {
	// Here we don't want the normal source filters because this is going to be mounted, we can filter
	// down to the requested path as part of a mount.
	//
	// We also don't need to rename anything since we are mounting it to a specific path.
	//
	// We do however need to handle any includes/excludes that are set so that we don't have more data
	// than expected in the mount.
	return func(st llb.State) llb.State {
		if len(opts.Includes) == 0 && len(opts.Excludes) == 0 {
			// if we have no includes or excludes, then we can just return the state as is
			return st
		}
		return llb.Scratch().File(llb.Copy(st, "/", "/", opts), opts.Constraints...)
	}
}

func (s *Source) fillDefaults() {
	s.toInterface().fillDefaults(s.Generate)
}

// doc writes should never error, so we panic if they do (and they won't because we are writing to a bytes.Buffer).
func printDocLn(w io.Writer, args ...any) {
	_, err := fmt.Fprintln(w, args...)
	if err != nil {
		panic(err)
	}
}

func printDocf(w io.Writer, format string, args ...any) {
	_, err := fmt.Fprintf(w, format, args...)
	if err != nil {
		panic(err)
	}
}

func (p *PatchSpec) UnmarshalYAML(ctx context.Context, node ast.Node) error {
	type internal PatchSpec
	var i internal

	dec := getDecoder(ctx)
	if err := dec.DecodeFromNodeContext(ctx, node, &i); err != nil {
		return errors.Wrap(err, "failed to decode patch spec")
	}
	*p = PatchSpec(i)
	p._sourceMap = newSourceMap(ctx, node)
	return nil
}
