package dalec

import (
	"bytes"
	"context"
	goerrors "errors"
	"fmt"
	"os"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
	"github.com/moby/buildkit/solver/errdefs"
	"github.com/pkg/errors"
	"golang.org/x/exp/maps"
)

const (
	// KeyDalecTarget is the key used the build arg name which may be used to read
	// the target name.
	KeyDalecTarget = "DALEC_TARGET"

	parseModeIgnoreComments = 0
)

func knownArg(key string) bool {

        // Allow sysext builder knobs to be passed without requiring every spec
        // to declare them under `args:`.
        if strings.HasPrefix(key, "DALEC_SYSEXT_") {
                return true
        }
	switch key {
	case "BUILDKIT_SYNTAX":
		return true
	case "DALEC_DISABLE_DIFF_MERGE":
		return true
	case "DALEC_SKIP_SIGNING":
		return true
	case "DALEC_SIGNING_CONFIG_CONTEXT_NAME":
		return true
	case "DALEC_SIGNING_CONFIG_PATH":
		return true
	case "DALEC_SKIP_TESTS":
		return true
	case KeyDalecTarget:
		return true
	}

	return platformArg(key)
}

func platformArg(key string) bool {
	switch key {
	case "TARGETOS", "TARGETARCH", "TARGETPLATFORM", "TARGETVARIANT",
		"BUILDOS", "BUILDARCH", "BUILDPLATFORM", "BUILDVARIANT":
		return true
	default:
		return false
	}
}

const DefaultPatchStrip int = 1

type envGetterMap map[string]string

func (m envGetterMap) Get(key string) (string, bool) {
	v, ok := m[key]
	return v, ok
}

func (m envGetterMap) Keys() []string {
	return maps.Keys(m)
}

func expandArgs(lex *shell.Lex, s string, args map[string]string, allowArg func(key string) bool) (string, error) {
	result, err := lex.ProcessWordWithMatches(s, envGetterMap(args))
	if err != nil {
		return "", err
	}

	var errs []error
	for m := range result.Unmatched {
		if !knownArg(m) && !allowArg(m) {
			errs = append(errs, fmt.Errorf(`build arg "%s" not declared`, m))
			continue
		}

		if platformArg(m) {
			errs = append(errs, fmt.Errorf(`opt-in arg "%s" not present in args`, m))
		}
	}

	return result.Result, errors.Wrap(goerrors.Join(errs...), "error performing variable expansion")
}

var errUnknownArg = errors.New("unknown arg")

type SubstituteConfig struct {
	AllowArg func(string) bool
}

// AllowAnyArg can be used to set [SubstituteConfig.AllowArg] to allow any arg
// to be substituted regardless of whether it is declared in the spec.
func AllowAnyArg(s string) bool {
	return true
}

// WithAllowAnyArg is a [SubstituteOpt] that sets [SubstituteConfig.AllowArg] to
// [AllowAnyArg].
func WithAllowAnyArg(cfg *SubstituteConfig) {
	cfg.AllowArg = AllowAnyArg
}

// DisallowAllUndeclared can be used to set [SubstituteConfig.AllowArg] to disallow args
// unless they are declared in the spec.
// This is used by default when substituting args.
func DisallowAllUndeclared(s string) bool {
	return false
}

// SubstituteOpt is a functional option for SubstituteArgs
type SubstituteOpt func(*SubstituteConfig)

func (s *Spec) SubstituteArgs(env map[string]string, opts ...SubstituteOpt) error {
	var cfg SubstituteConfig

	cfg.AllowArg = DisallowAllUndeclared

	for _, o := range opts {
		o(&cfg)
	}

	lex := shell.NewLex('\\')
	// force the shell lexer to skip unresolved env vars so they aren't
	// replaced with ""
	lex.SkipUnsetEnv = true

	var errs []error
	appendErr := func(err error) {
		errs = append(errs, err)
	}

	args := make(map[string]string)
	for k, v := range s.Args {
		args[k] = v
	}

	for k, v := range env {
		if k == "SOURCE_DATE_EPOCH" {
			args[k] = v
			continue
		}

		if _, ok := args[k]; !ok {
			if !knownArg(k) && !cfg.AllowArg(k) {
				appendErr(fmt.Errorf("%w: %q", errUnknownArg, k))
			}

			// if the build arg isn't present in args by opt-in, skip
			continue
		}

		args[k] = v
	}

	// If SOURCE_DATE_EPOCH is available as a build arg, ensure it's also available
	// as an environment variable in the build environment
	if epochValue, ok := args["SOURCE_DATE_EPOCH"]; ok {
		if s.Build.Env == nil {
			s.Build.Env = make(map[string]string)
		}
		if _, exists := s.Build.Env["SOURCE_DATE_EPOCH"]; !exists {
			s.Build.Env["SOURCE_DATE_EPOCH"] = epochValue
		}
	}

	for name, src := range s.Sources {
		if err := src.processBuildArgs(lex, args, cfg.AllowArg); err != nil {
			appendErr(errors.Wrapf(err, "source %q", name))
		}
		s.Sources[name] = src
	}

	for src, patchList := range s.Patches {
		for i, patch := range patchList {
			updated, err := expandArgs(lex, patch.Path, args, cfg.AllowArg)
			if err != nil {
				appendErr(errors.Wrapf(err, "patch %s path %d", src, i))
			}
			s.Patches[src][i].Path = updated
		}
	}

	updated, err := expandArgs(lex, s.Version, args, cfg.AllowArg)
	if err != nil {
		appendErr(errors.Wrap(err, "version"))
	}
	s.Version = updated

	updated, err = expandArgs(lex, s.Revision, args, cfg.AllowArg)
	if err != nil {
		appendErr(errors.Wrap(err, "revision"))
	}
	s.Revision = updated

	if err := s.Build.processBuildArgs(lex, args, cfg.AllowArg); err != nil {
		appendErr(errors.Wrap(err, "build"))
	}

	if s.Build.NetworkMode != "" {
		updated, err := expandArgs(lex, s.Build.NetworkMode, args, cfg.AllowArg)
		if err != nil {
			appendErr(fmt.Errorf("error performing shell expansion on build network mode: %s: %w", s.Build.NetworkMode, err))
		}
		s.Build.NetworkMode = updated
	}

	for i, step := range s.Build.Steps {
		bs := &step
		if err := bs.processBuildArgs(lex, args, cfg.AllowArg); err != nil {
			appendErr(errors.Wrapf(err, "step index %d", i))
		}
		s.Build.Steps[i] = *bs
	}

	for _, t := range s.Tests {
		if err := t.processBuildArgs(lex, args, cfg.AllowArg); err != nil {
			appendErr(err)
		}
	}

	for name, t := range s.Targets {
		if err := t.processBuildArgs(lex, args, cfg.AllowArg); err != nil {
			appendErr(errors.Wrapf(err, "target %s", name))
		}
		s.Targets[name] = t
	}

	if s.PackageConfig != nil {
		if err := s.PackageConfig.processBuildArgs(lex, args, cfg.AllowArg); err != nil {
			appendErr(errors.Wrap(err, "package config"))
		}
	}

	if err := s.Image.processBuildArgs(lex, args, cfg.AllowArg); err != nil {
		appendErr(errors.Wrap(err, "package config"))
	}

	if err := s.Dependencies.processBuildArgs(lex, args, cfg.AllowArg); err != nil {
		appendErr(errors.Wrap(err, "dependencies"))
	}

	for k, v := range s.Provides {
		for i, ver := range v.Version {
			updated, err := expandArgs(lex, ver, args, cfg.AllowArg)
			if err != nil {
				appendErr(errors.Wrapf(err, "provides %s version %d", k, i))
			}
			s.Provides[k].Version[i] = updated
		}
	}

	for k, v := range s.Replaces {
		for i, ver := range v.Version {
			updated, err := expandArgs(lex, ver, args, cfg.AllowArg)
			if err != nil {
				appendErr(errors.Wrapf(err, "replaces %s version %d", k, i))
			}
			s.Replaces[k].Version[i] = updated
		}
	}

	for k, v := range s.Conflicts {
		for i, ver := range v.Version {
			updated, err := expandArgs(lex, ver, args, cfg.AllowArg)
			if err != nil {
				appendErr(errors.Wrapf(err, "replaces %s version %d", k, i))
			}
			s.Conflicts[k].Version[i] = updated
		}
	}

	return goerrors.Join(errs...)
}

// LoadSpec loads a spec from the given data.
func LoadSpec(dt []byte) (*Spec, error) {
	return loadSpec(context.TODO(), dt)
}

func loadSpec(ctx context.Context, dt []byte) (*Spec, error) {
	var spec Spec

	spec.decodeOpts = append(spec.decodeOpts, yaml.Strict())
	if err := yaml.UnmarshalContext(ctx, dt, &spec, append(spec.decodeOpts, yaml.AllowFieldPrefixes("x-", "X-"))...); err != nil {
		return nil, errors.Wrap(err, "error unmarshalling spec")
	}

	if err := spec.Validate(); err != nil {
		return nil, err
	}

	spec.FillDefaults()
	return &spec, nil
}

// LoadSpecWithSourceMap loads a spec from the given data and attaches source map information to the spec.
func LoadSpecWithSourceMap(filename string, dt []byte) (*Spec, error) {
	ctx := context.WithValue(context.TODO(), sourceMapContextKey{}, sourceMapContext{
		fileName: filename,
		data:     dt,
		language: "yaml",
	})

	spec, err := loadSpec(ctx, dt)
	if err != nil {
		return nil, err
	}
	return spec, nil
}

type baseDocKey struct{}

func withBaseDoc(ctx context.Context, node ast.Node) context.Context {
	return context.WithValue(ctx, baseDocKey{}, node)
}

func getBaseDoc(ctx context.Context) ast.Node {
	v := ctx.Value(baseDocKey{})
	if v == nil {
		return nil
	}
	return v.(ast.Node)
}

type decodeOptsKey struct{}

func contextWithDecodeOpts(ctx context.Context, opts []yaml.DecodeOption) context.Context {
	if len(opts) == 0 {
		return ctx
	}
	return context.WithValue(ctx, decodeOptsKey{}, opts)
}

func decodeOpts(ctx context.Context) []yaml.DecodeOption {
	v := ctx.Value(decodeOptsKey{})
	if v == nil {
		return nil
	}
	return v.([]yaml.DecodeOption)
}

func getDecoder(ctx context.Context) *yaml.Decoder {
	opts := decodeOpts(ctx)
	base := getBaseDoc(ctx)
	if base != nil {
		opts = append(opts, yaml.ReferenceReaders(base))
	}
	return yaml.NewDecoder(bytes.NewBuffer(nil), opts...)
}

// UnmarshalYAML implements the [yaml.NodeUnmarshaler] interface to provide custom unmarshalling.
func (s *Spec) UnmarshalYAML(ctx context.Context, node ast.Node) error {
	if node.Type() == ast.NullType {
		*s = Spec{}
		return nil
	}

	type internalSpec Spec
	var s2 internalSpec

	ctx = contextWithDecodeOpts(ctx, s.decodeOpts)
	ctx = withBaseDoc(ctx, node)

	opts := append(s.decodeOpts, yaml.AllowFieldPrefixes("x-", "X-"))

	var buf bytes.Buffer
	dec := yaml.NewDecoder(&buf, opts...)

	err := dec.DecodeFromNodeContext(ctx, node, &s2)
	if err != nil {
		return errors.Wrap(err, "error unmarshalling to spec")
	}

	*s = Spec(s2)

	var ext extensionFields
	if err := yaml.NodeToValue(node, &ext, opts...); err != nil {
		return errors.Wrap(err, "error unmarshalling extension nodes")
	}

	s.extensions = ext

	return nil
}

func (s Spec) MarshalYAML() ([]byte, error) {
	dtExt, err := yaml.Marshal(s.extensions)
	if err != nil {
		return nil, errors.Wrap(err, "error marshaling extensions")
	}

	parsedExt, err := parser.ParseBytes(dtExt, 0)
	if err != nil {
		return nil, errors.Wrapf(err, "error parsing yaml: \n%s", string(dtExt))
	}

	type internalSpec Spec
	ss := internalSpec(s)

	dt, err := yaml.Marshal(ss)
	if err != nil {
		return nil, errors.Wrap(err, "error marshaling spec")
	}

	parsed, err := parser.ParseBytes(dt, parser.ParseComments)
	if err != nil {
		return nil, errors.Wrap(err, "error re-parsing spec yaml")
	}

	p, err := yaml.PathString("$")
	if err != nil {
		return nil, errors.Wrap(err, "error creating path selector")
	}
	if err := p.MergeFromFile(parsed, parsedExt); err != nil {
		return nil, errors.Wrap(err, "error merging extension nodes")
	}

	return []byte(parsed.String()), nil
}

func (s *BuildStep) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(string) bool) error {
	var errs []error
	for k, v := range s.Env {
		updated, err := expandArgs(lex, v, args, allowArg)
		if err != nil {
			errs = append(errs, errors.Wrapf(err, "env %s=%s", k, v))
		}
		s.Env[k] = updated
	}
	return goerrors.Join(errs...)
}

func (c *Command) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(string) bool) error {
	if c == nil {
		return nil
	}

	newLex := *lex
	newLex.SkipUnsetEnv = true // skip unset env vars so they aren't replaced with ""
	lex = &newLex

	var errs []error
	appendErr := func(err error) {
		errs = append(errs, err)
	}

	for i, s := range c.Mounts {
		if err := s.processBuildArgs(lex, args, allowArg); err != nil {
			appendErr(err)
			continue
		}
		c.Mounts[i] = s
	}

	for k, v := range c.Env {
		updated, err := expandArgs(lex, v, args, allowArg)
		if err != nil {
			appendErr(errors.Wrapf(err, "env %s=%v", k, v))
			continue
		}
		c.Env[k] = updated
	}
	for i, step := range c.Steps {
		if err := step.processBuildArgs(lex, args, allowArg); err != nil {
			appendErr(errors.Wrapf(err, "step index %d", i))

		}
		for k, v := range step.Env {
			updated, err := expandArgs(lex, v, args, allowArg)
			if err != nil {
				appendErr(errors.Wrapf(err, "step env %s=%s", k, v))
				continue
			}

			step.Env[k] = updated
			c.Steps[i] = step
		}
	}

	return goerrors.Join(errs...)
}

func (s *Spec) FillDefaults() {
	for name, src := range s.Sources {
		ss := &src
		ss.fillDefaults()
		s.Sources[name] = *ss
	}

	for k, patches := range s.Patches {
		for i, ps := range patches {
			if ps.Strip != nil {
				continue
			}
			strip := DefaultPatchStrip
			s.Patches[k][i].Strip = &strip
		}
	}

	s.Dependencies.fillDefaults()
	s.Image.fillDefaults()

	for k := range s.Targets {
		t := s.Targets[k]
		t.fillDefaults()
		s.Targets[k] = t
	}

	s.Image.fillDefaults()
}

func (s Spec) Validate() error {
	var errs []error

	for name, src := range s.Sources {
		if strings.ContainsRune(name, os.PathSeparator) {
			errs = append(errs, &InvalidSourceError{Name: name, Err: errSourceNamePathSeparator})
		}
		if err := src.validate(); err != nil {
			errs = append(errs, &InvalidSourceError{Name: name, Err: err})
		}
	}

	for _, t := range s.Tests {
		if err := t.validate(); err != nil {
			errs = append(errs, errors.Wrap(err, t.Name))
		}
	}

	for src, patches := range s.Patches {
		for _, patch := range patches {
			patchSrc, ok := s.Sources[patch.Source]
			if !ok {
				var err error = &InvalidPatchError{Source: src, PatchSpec: &patch, Err: errMissingSource}
				err = errdefs.WithSource(err, patch._sourceMap.GetErrdefsSource())
				errs = append(errs, err)
				continue
			}

			if err := validatePatch(patch, patchSrc); err != nil {
				err = &InvalidPatchError{Source: src, PatchSpec: &patch, Err: err}
				err = errdefs.WithSource(err, patch._sourceMap.GetErrdefsSource())
				errs = append(errs, err)
			}
		}
	}

	if err := s.Dependencies.validate(); err != nil {
		errs = append(errs, errors.Wrap(err, "dependencies"))
	}

	if err := s.Image.validate(); err != nil {
		errs = append(errs, errors.Wrap(err, "image"))
	}

	for k, t := range s.Targets {
		if err := t.validate(); err != nil {
			errs = append(errs, errors.Wrapf(err, "target %s", k))
		}
	}

	if err := s.Image.validate(); err != nil {
		errs = append(errs, errors.Wrap(err, "image"))
	}

	if err := s.Build.validate(); err != nil {
		errs = append(errs, errors.Wrap(err, "build"))
	}

	if err := s.Artifacts.validate(); err != nil {
		errs = append(errs, errors.Wrap(err, "artifacts"))
	}

	return goerrors.Join(errs...)
}

func validatePatch(patch PatchSpec, patchSrc Source) error {
	if SourceIsDir(patchSrc) {
		// Patch sources that use directory-backed sources require a subpath in the
		// patch spec.
		if isRoot(patch.Path) {
			return errPatchRequiresSubpath
		}
		return nil
	}

	// File backed sources with a subpath in the patch spec is invalid since it is
	// already a file, not a directory.
	if !isRoot(patch.Path) {
		return errPatchFileNoSubpath
	}
	return nil
}

func (g *SourceGenerator) Validate() error {
	if g.Gomod == nil && g.Cargohome == nil && g.Pip == nil && g.NodeMod == nil {
		// Gomod, Cargohome, Pip, and NodeMod are the only valid generator types
		// An empty generator is invalid
		return fmt.Errorf("no generator type specified")
	}
	if err := g.Gomod.validate(); err != nil {
		return errors.Wrap(err, "gomod generator")
	}

	return nil
}

func (g *GeneratorGomod) validate() error {
	// Validate gomod edits if present
	if g != nil && g.Edits != nil {
		var errs []error
		for i, rep := range g.Edits.Replace {
			if _, err := rep.goModEditArg(); err != nil {
				errs = append(errs, errors.Wrapf(err, "replace[%d]", i))
			}
		}
		if err := goerrors.Join(errs...); err != nil {
			return errors.Wrap(err, "gomod edits")
		}
	}
	return nil
}

func (s *PackageSigner) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(string) bool) error {
	var errs []error
	for k, v := range s.Args {
		updated, err := expandArgs(lex, v, args, allowArg)
		if err != nil {
			errs = append(errs, errors.Wrapf(err, "arg %s=%s", k, v))
			continue
		}
		s.Args[k] = updated
	}
	return goerrors.Join(errs...)
}

func (cfg *PackageConfig) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(string) bool) error {
	if cfg.Signer != nil {
		if err := cfg.Signer.processBuildArgs(lex, args, allowArg); err != nil {
			return errors.Wrap(err, "signer")
		}
	}

	return nil
}

func (b *ArtifactBuild) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(string) bool) error {
	var errs []error

	for k, v := range b.Env {
		updated, err := expandArgs(lex, v, args, allowArg)
		if err != nil {
			errs = append(errs, errors.Wrapf(err, "env %s=%s", k, v))
			continue
		}
		b.Env[k] = updated
	}

	return goerrors.Join(errs...)
}

func validateSymlinks(symlinks map[string]SymlinkTarget) error {
	var (
		errs     []error
		numPairs int
	)

	for oldpath, cfg := range symlinks {
		var err error
		if oldpath == "" {
			err = fmt.Errorf("symlink source is empty")
			errs = append(errs, err)
		}

		if (cfg.Path != "" && len(cfg.Paths) > 0) || (cfg.Path == "" && len(cfg.Paths) == 0) {
			err = fmt.Errorf("'path' and 'paths' fields are mutually exclusive, and at least one is required: "+
				"symlink to %s", oldpath)

			errs = append(errs, err)
		}

		if err != nil {
			continue
		}

		if cfg.Path != "" { // this means .Paths is empty
			numPairs++
			continue
		}

		for _, newpath := range cfg.Paths { // this means .Path is empty
			numPairs++
			if newpath == "" {
				errs = append(errs, fmt.Errorf("symlink newpath should not be empty"))
				continue
			}
		}
	}

	// The remainder of this function checks for duplicate `newpath`s in the
	// symlink pairs. This is not allowed: neither the ordering of the
	// `oldpath` map keys, nor that of the `.Paths` values can be trusted. We
	// also sort both to avoid cache misses, so we would end up with
	// inconsistent behavior -- regardless of whether the inputs are the same.
	if numPairs < 2 {
		return goerrors.Join(errs...)
	}

	var (
		oldpath string
		cfg     SymlinkTarget
	)

	seen := make(map[string]string, numPairs)
	checkDuplicateNewpath := func(newpath string) {
		if newpath == "" {
			return
		}

		if seenPath, found := seen[newpath]; found {
			errs = append(errs, fmt.Errorf("symlink 'newpaths' must be unique: %q points to both %q and %q",
				newpath, oldpath, seenPath))
		}

		seen[newpath] = oldpath
	}

	for oldpath, cfg = range symlinks {
		checkDuplicateNewpath(cfg.Path)

		for _, newpath := range cfg.Paths {
			checkDuplicateNewpath(newpath)
		}
	}

	return goerrors.Join(errs...)
}

func (img *ImageConfig) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(string) bool) error {
	if img == nil {
		return nil
	}

	var errs error

	for k, v := range img.Labels {
		updated, err := expandArgs(lex, v, args, allowArg)
		if err != nil {
			errs = goerrors.Join(errs, errors.Wrapf(err, "env %s=%s", k, v))
			continue
		}
		img.Labels[k] = updated
	}

	return errs
}

func (b ArtifactBuild) validate() error {
	var errs []error

	if len(b.Steps) == 0 {
		// If there are no steps but other fields are set, that is likely uninentional.
		if len(b.Caches) > 0 || len(b.Env) > 0 || len(b.NetworkMode) > 0 {
			errs = append(errs, fmt.Errorf("artifact build must have at least one step"))
		}
	}

	for i, step := range b.Steps {
		if err := step.validate(); err != nil {
			errs = append(errs, errors.Wrapf(err, "step %d", i))
		}
	}

	switch b.NetworkMode {
	case "", netModeNone, netModeSandbox:
	default:
		errs = append(errs, fmt.Errorf("invalid network mode: %q: valid values %s", b.NetworkMode, []string{netModeNone, netModeSandbox}))
	}

	haveGo := false
	for i, cache := range b.Caches {
		if cache.GoBuild != nil {
			if haveGo {
				errs = append(errs, fmt.Errorf("only one gobuild cache is allowed"))
			}
			haveGo = true
		}
		if err := cache.validate(); err != nil {
			errs = append(errs, errors.Wrapf(err, "cache %d", i))
		}
	}

	return goerrors.Join(errs...)
}

// errIsOnly checks if the error is the only instance of the target error in the
// error chain.
// This is useful when we want to know if a specific error is the only one.
func errorIsOnly(err error, target error) bool {
	if err == nil {
		return target == nil
	}
	if target == nil {
		return false
	}

	type joinedError interface {
		Unwrap() []error
	}

	joined, ok := err.(joinedError)
	if !ok {
		return errors.Is(err, target)
	}

	var count int
	for _, e := range joined.Unwrap() {
		if errors.Is(e, target) {
			count++
		}
		if count > 1 {
			return false
		}
	}

	return count == 1
}

func (f *extensionFields) UnmarshalYAML(node ast.Node) error {
	body, ok := node.(*ast.MappingNode)
	if !ok {
		if node.Type() == ast.NullType {
			return nil
		}
		return errors.Errorf("expected a mapping node, got %T", node)
	}

	ext := *f
	for _, v := range body.Values {
		key := v.Key.String()
		if !strings.HasPrefix(key, "x-") && !strings.HasPrefix(key, "X-") {
			continue
		}

		if ext == nil {
			ext = make(extensionFields)
		}
		ext[key] = v.Value
	}

	if ext != nil {
		*f = ext
	}
	return nil
}
