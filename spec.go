//go:generate go run ./cmd/gen-jsonschema docs/spec.schema.json
package dalec

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"
)

// Spec is the specification for a package build.
type Spec struct {
	// Name is the name of the package.
	Name string `yaml:"name" json:"name" jsonschema:"required"`
	// Description is a short description of the package.
	Description string `yaml:"description" json:"description" jsonschema:"required"`
	// Website is the URL to store in the metadata of the package.
	Website string `yaml:"website" json:"website"`

	// Version sets the version of the package.
	Version string `yaml:"version" json:"version" jsonschema:"required"`
	// Revision sets the package revision.
	// This will generally get merged into the package version when generating the package.
	Revision string `yaml:"revision" json:"revision" jsonschema:"required,oneof_type=string;integer"`

	// Marks the package as architecture independent.
	// It is up to the package author to ensure that the package is actually architecture independent.
	// This is metadata only.
	NoArch bool `yaml:"noarch,omitempty" json:"noarch,omitempty"`

	// Conflicts is the list of packages that conflict with the generated package.
	// This will prevent the package from being installed if any of these packages are already installed or vice versa.
	Conflicts PackageDependencyList `yaml:"conflicts,omitempty" json:"conflicts,omitempty"`
	// Replaces is the list of packages that are replaced by the generated package.
	Replaces PackageDependencyList `yaml:"replaces,omitempty" json:"replaces,omitempty"`
	// Provides is the list of things that the generated package provides.
	// This can be used to satisfy dependencies of other packages.
	// As an example, the moby-runc package provides "runc", other packages could depend on "runc" and be satisfied by moby-runc.
	// This is an advanced use case and consideration should be taken to ensure that the package actually provides the thing it claims to provide.
	Provides PackageDependencyList `yaml:"provides,omitempty" json:"provides,omitempty"`

	// Sources is the list of sources to use to build the artifact(s).
	// The map key is the name of the source and the value is the source configuration.
	// The source configuration is used to fetch the source and filter the files to include/exclude.
	// This can be mounted into the build using the "Mounts" field in the StepGroup.
	//
	// Sources can be embedded in the main spec as here or overridden in a build request.
	Sources map[string]Source `yaml:"sources,omitempty" json:"sources,omitempty"`

	// Patches is the list of patches to apply to the sources.
	// The map key is the name of the source to apply the patches to.
	// The value is the list of patches to apply to the source.
	// The patch must be present in the `Sources` map.
	// Each patch is applied in order and the result is used as the source for the build.
	Patches map[string][]PatchSpec `yaml:"patches,omitempty" json:"patches,omitempty"`

	// Build is the configuration for building the artifacts in the package.
	Build ArtifactBuild `yaml:"build,omitempty" json:"build,omitempty"`

	// Args is the list of arguments that can be used for shell-style expansion in (certain fields of) the spec.
	// Any arg supplied in the build request which does not appear in this list will cause an error.
	// Attempts to use an arg in the spec which is not specified here will assume to be a literal string.
	// The map value is the default value to use if the arg is not supplied in the build request.
	Args map[string]string `yaml:"args,omitempty" json:"args,omitempty"`

	// License is the license of the package.
	License string `yaml:"license" json:"license"`
	// Vendor is the vendor of the package.
	Vendor string `yaml:"vendor,omitempty" json:"vendor,omitempty"`
	// Packager is the name of the person,team,company that packaged the package.
	Packager string `yaml:"packager,omitempty" json:"packager,omitempty"`

	// Artifacts is the list of artifacts to include in the package.
	Artifacts Artifacts `yaml:"artifacts,omitempty" json:"artifacts,omitempty"`

	// The list of distro targets to build the package for.
	Targets map[string]Target `yaml:"targets,omitempty" json:"targets,omitempty"`

	// Dependencies are the different dependencies that need to be specified in the package.
	// Dependencies are overwritten if specified in the target map for the requested distro.
	Dependencies *PackageDependencies `yaml:"dependencies,omitempty" json:"dependencies,omitempty"`
	// PackageConfig is the configuration to use for artifact targets, such as
	// rpms, debs, or zip files containing Windows binaries
	PackageConfig *PackageConfig `yaml:"package_config,omitempty" json:"package_config,omitempty"`
	// Image is the image configuration when the target output is a container image.
	// This is overwritten if specified in the target map for the requested distro.
	Image *ImageConfig `yaml:"image,omitempty" json:"image,omitempty"`

	// Changelog is the list of changes to the package.
	Changelog []ChangelogEntry `yaml:"changelog,omitempty" json:"changelog,omitempty"`

	// Tests are the list of tests to run for the package that should work regardless of target OS
	// Each item in this list is run with a separate rootfs and cannot interact with other tests.
	// Each [TestSpec] is run with a separate rootfs, asynchronously from other [TestSpec].
	Tests []*TestSpec `yaml:"tests,omitempty" json:"tests,omitempty"`

	extensions extensionFields `yaml:"-" json:"-"`

	decodeOpts []yaml.DecodeOption `yaml:"-" json:"-"`

	// (previously used for side-table source maps) removed - per-object constraints stored on objects
	gomodPatches          map[string][]*GomodPatch `yaml:"-" json:"-"`
	gomodPatchesGenerated bool                     `yaml:"-" json:"-"`
}

// extensionFields is a map for storing extension fields in the spec.
type extensionFields map[string]ast.Node

// PatchSpec is used to apply a patch to a source with a given set of options.
// This is used in [Spec.Patches]
type PatchSpec struct {
	// Source is the name of the source that contains the patch to apply.
	Source string `yaml:"source" json:"source" jsonschema:"required"`
	// Strip is the number of leading path components to strip from the patch.
	// The default is 1 which is typical of a git diff.
	Strip *int `yaml:"strip,omitempty" json:"strip,omitempty"`
	// Optional subpath to the patch file inside the source
	// This is only useful for directory-backed sources.
	Path string `yaml:"path,omitempty" json:"path,omitempty"`

	_sourceMap *sourceMap `json:"-" yaml:"-"`
}

// ChangelogEntry is an entry in the changelog.
// This is used to generate the changelog for the package.
type ChangelogEntry struct {
	// Date is the date of the changelog entry.
	// Dates are formatted as YYYY-MM-DD
	Date Date `yaml:"date" json:"date" jsonschema:"oneof_required=date"`
	// Author is the author of the changelog entry. e.g. `John Smith <john.smith@example.com>`
	Author string `yaml:"author" json:"author"`
	// Changes is the list of changes in the changelog entry.
	Changes []string `yaml:"changes" json:"changes"`
}

type Date struct {
	time.Time
}

func (d Date) Compare(other Date) int {
	return d.Time.Compare(other.Time)
}

func (d Date) MarshalYAML() ([]byte, error) {
	return yaml.Marshal(d.String())
}

func (d *Date) UnmarshalYAML(dt []byte) error {
	var s string
	if err := yaml.Unmarshal(dt, &s); err != nil {
		return errors.Wrap(err, "error unmarshalling date to string")
	}
	parsedTime, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return err
	}
	d.Time = parsedTime
	return nil
}

func (d Date) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

func (d *Date) UnmarshalJSON(dt []byte) error {
	var s string
	if err := json.Unmarshal(dt, &s); err != nil {
		return err
	}
	parsedTime, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return err
	}
	d.Time = parsedTime
	return nil
}

func (d Date) String() string {
	return d.Format(time.DateOnly)
}

// PostInstall is the post install configuration for the image.
type PostInstall struct {
	// Symlinks is the list of symlinks to create in the container rootfs after the package(s) are installed.
	// The key is the path the symlink should point to.
	Symlinks map[string]SymlinkTarget `yaml:"symlinks,omitempty" json:"symlinks,omitempty"`
}

// SymlinkTarget specifies the properties of a symlink
type SymlinkTarget struct {
	// Path is the path where the symlink should be placed
	//
	// Deprecated: This is here for backward compatibility. Use `Paths` instead.
	Path string `yaml:"path" json:"path" jsonschema:"oneof_required=path"`
	// Path is a list of `newpath`s that will all point to the same `oldpath`.
	Paths []string `yaml:"paths" json:"paths" jsonschema:"oneof_required=paths"`
	// User is the user name to set on the symlink.
	User string `yaml:"user,omitempty" json:"user,omitempty"`
	// Group is the group name to set on the symlink.
	Group string `yaml:"group,omitempty" json:"group,omitempty"`
}

// GeneratorGomod is used to generate a go module cache from go module sources
type GeneratorGomod struct {
	// Paths is the list of paths to run the generator on. Used to generate multi-module in a single source.
	Paths []string `yaml:"paths,omitempty" json:"paths,omitempty"`
	// Auth is the git authorization to use for gomods. The keys are the hosts, and the values are the auth to use for that host.
	Auth map[string]GomodGitAuth `yaml:"auth,omitempty" json:"auth,omitempty"`
	// Replace applies go.mod replace directives before downloading module dependencies.
	// Each entry must be of the form "old:new" where "old" and "new" match the syntax accepted by
	// `go mod edit -replace`.
	Replace []GomodReplace `yaml:"replace,omitempty" json:"replace,omitempty"`
	// Require applies go.mod require directives before downloading module dependencies.
	// Each entry must be of the form "module:target@version" to match the syntax accepted by
	// `go mod edit -require`.
	Require []GomodRequire `yaml:"require,omitempty" json:"require,omitempty"`

	_sourceMap *sourceMap `yaml:"-" json:"-"`
}

// GomodReplace represents a go.mod replace directive in shorthand "old:new" form.
// This allows users to substitute module dependencies before go mod download runs,
// useful for pointing to local forks or alternate versions.
type GomodReplace string

func (r GomodReplace) String() string {
	return string(r)
}

func parseGomodReplace(value string) (string, string, error) {
	parts := strings.SplitN(value, ":", 2)
	if len(parts) != 2 {
		return "", "", errors.Errorf("invalid gomod replace %q, expected format old:new", value)
	}
	old := strings.TrimSpace(parts[0])
	new := strings.TrimSpace(parts[1])
	if old == "" || new == "" {
		return "", "", errors.Errorf("invalid gomod replace %q, entries must be non-empty", value)
	}
	return old, new, nil
}

func normalizeGomodReplace(old, new string) GomodReplace {
	return GomodReplace(strings.TrimSpace(old) + ":" + strings.TrimSpace(new))
}

func (r GomodReplace) parts() (string, string, error) {
	return parseGomodReplace(string(r))
}

func (r GomodReplace) goModEditArg() (string, error) {
	old, new, err := r.parts()
	if err != nil {
		return "", err
	}
	return old + "=" + new, nil
}

func (r *GomodReplace) UnmarshalYAML(b []byte) error {
	var raw string
	if err := yaml.Unmarshal(b, &raw); err != nil {
		return err
	}
	old, new, err := parseGomodReplace(raw)
	if err != nil {
		return err
	}
	*r = normalizeGomodReplace(old, new)
	return nil
}

func (r *GomodReplace) UnmarshalJSON(b []byte) error {
	var raw string
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	old, new, err := parseGomodReplace(raw)
	if err != nil {
		return err
	}
	*r = normalizeGomodReplace(old, new)
	return nil
}

// GomodRequire represents a go.mod require directive in shorthand "module:target@version" form.
// This allows changing the resolved version of a dependency, including sourcing from a fork
// while maintaining the original module path in go.mod.
type GomodRequire string

func (r GomodRequire) String() string {
	return string(r)
}

func parseGomodRequire(value string) (string, string, error) {
	parts := strings.SplitN(value, ":", 2)
	if len(parts) != 2 {
		return "", "", errors.Errorf("invalid gomod require %q, expected format module:target@version", value)
	}
	module := strings.TrimSpace(parts[0])
	target := strings.TrimSpace(parts[1])
	if module == "" || target == "" {
		return "", "", errors.Errorf("invalid gomod require %q, entries must be non-empty", value)
	}
	return module, target, nil
}

func normalizeGomodRequire(module, target string) GomodRequire {
	return GomodRequire(strings.TrimSpace(module) + ":" + strings.TrimSpace(target))
}

func (r GomodRequire) parts() (string, string, error) {
	return parseGomodRequire(string(r))
}

func (r GomodRequire) goModEditArg() (string, error) {
	_, target, err := r.parts()
	if err != nil {
		return "", err
	}
	if !strings.Contains(target, "@") {
		return "", errors.Errorf("invalid gomod require %q, target must include @version", target)
	}
	return target, nil
}

func (r *GomodRequire) UnmarshalYAML(b []byte) error {
	var raw string
	if err := yaml.Unmarshal(b, &raw); err != nil {
		return err
	}
	module, target, err := parseGomodRequire(raw)
	if err != nil {
		return err
	}
	*r = normalizeGomodRequire(module, target)
	return nil
}

func (r *GomodRequire) UnmarshalJSON(b []byte) error {
	var raw string
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	module, target, err := parseGomodRequire(raw)
	if err != nil {
		return err
	}
	*r = normalizeGomodRequire(module, target)
	return nil
}

// GeneratorCargohome is used to generate a cargo home from cargo sources
type GeneratorCargohome struct {
	// Paths is the list of paths to run the generator on. Used to generate multi-module in a single source.
	Paths []string `yaml:"paths,omitempty" json:"paths,omitempty"`

	_sourceMap *sourceMap `yaml:"-" json:"-"`
}

type GeneratorPip struct {
	// Paths is the list of paths to run the generator on. Used to generate multi-module in a single source.
	Paths []string `yaml:"paths,omitempty" json:"paths,omitempty"`

	// RequirementsFile is the name of the requirements file (default: "requirements.txt")
	RequirementsFile string `yaml:"requirements_file,omitempty" json:"requirements_file,omitempty"`

	// IndexUrl specifies a custom PyPI index URL
	IndexUrl string `yaml:"index_url,omitempty" json:"index_url,omitempty"`

	// ExtraIndexUrls specifies additional PyPI index URLs
	ExtraIndexUrls []string `yaml:"extra_index_urls,omitempty" json:"extra_index_urls,omitempty"`

	_sourceMap *sourceMap `yaml:"-" json:"-"`
}

// GeneratorNodeMod is used to generate a node module cache for Yarn or npm.
type GeneratorNodeMod struct {
	// Paths is the list of paths to run the generator on. Used to generate multi-module in a single source.
	Paths []string `yaml:"paths,omitempty" json:"paths,omitempty"`

	_sourceMap *sourceMap `yaml:"-" json:"-"`
}

// SourceGenerator holds the configuration for a source generator.
// This can be used inside of a [Source] to generate additional sources from the given source.
type SourceGenerator struct {
	// Subpath is the path inside a source to run the generator from.
	Subpath string `yaml:"subpath,omitempty" json:"subpath,omitempty"`

	// Gomod is the go module generator.
	Gomod *GeneratorGomod `yaml:"gomod,omitempty" json:"gomod,omitempty" jsonschema:"oneof_required=gomod"`

	// Cargohome is the cargo home generator.
	Cargohome *GeneratorCargohome `yaml:"cargohome,omitempty" json:"cargohome,omitempty" jsonschema:"oneof_required=cargohome"`

	// Pip is the pip generator.
	Pip *GeneratorPip `yaml:"pip,omitempty" json:"pip,omitempty" jsonschema:"oneof_required=pip"`

	// NodeMod is the generic node module generator for npm.
	NodeMod *GeneratorNodeMod `yaml:"nodemod,omitempty" json:"nodemod,omitempty" jsonschema:"oneof_required=nodemod"`
}

// ArtifactBuild configures a group of steps that are run sequentially along with their outputs to build the artifact(s).
type ArtifactBuild struct {
	// Steps is the list of commands to run to build the artifact(s).
	// Each step is run sequentially and will be cached accordingly depending on the frontend implementation.
	Steps BuildStepList `yaml:"steps" json:"steps" jsonschema:"required"`
	// Env is the list of environment variables to set for all commands in this step group.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`

	// NetworkMode sets the network mode to use during the build phase.
	// Accepted values: none, sandbox
	// Default: none
	NetworkMode string `yaml:"network_mode,omitempty" json:"network_mode,omitempty" jsonschema:"enum=none,enum=sandbox"`

	// Caches is the list of caches to use for the build.
	// These apply to all steps.
	Caches []CacheConfig `yaml:"caches,omitempty" json:"caches,omitempty"`
}

type BuildStepList []BuildStep

func (ls *BuildStepList) UnmarshalYAML(ctx context.Context, node ast.Node) error {
	seq, ok := node.(*ast.SequenceNode)
	if !ok {
		return errors.New("expected sequence node for build steps")
	}

	result := make([]BuildStep, 0, len(seq.Values))
	for _, n := range seq.Values {
		var step BuildStep

		if err := yaml.NodeToValue(n, &step, decodeOpts(ctx)...); err != nil {
			return err
		}
		step._sourceMap = newSourceMap(ctx, n)
		result = append(result, step)
	}

	*ls = result
	return nil
}

func (ls BuildStepList) GetSourceLocation(st llb.State) llb.ConstraintsOpt {
	if len(ls) == 0 {
		return ConstraintsOptFunc(func(c *llb.Constraints) {})
	}

	locs := make([]llb.ConstraintsOpt, 0, len(ls))
	for _, step := range ls {
		if c := step.GetSourceLocation(st); c != nil {
			locs = append(locs, c)
		}
	}
	return MergeSourceLocations(locs...)
}

// Frontend encapsulates the configuration for a frontend to forward a build target to.
type Frontend struct {
	// Image specifies the frontend image to forward the build to.
	// This can be left unspecified *if* the original frontend has builtin support for the distro.
	//
	// If the original frontend does not have builtin support for the distro, this must be specified or the build will fail.
	// If this is specified then it MUST be used.
	Image string `yaml:"image,omitempty" json:"image,omitempty" jsonschema:"required,example=docker.io/my/frontend:latest"`
	// CmdLine is the command line to use to forward the build to the frontend.
	// By default the frontend image's entrypoint/cmd is used.
	CmdLine string `yaml:"cmdline,omitempty" json:"cmdline,omitempty"`
}

// PackageSigner is the configuration for defining how to sign a package
type PackageSigner struct {
	*Frontend `yaml:",inline" json:",inline"`
	// Args are passed along to the signer frontend as build args
	Args map[string]string `yaml:"args,omitempty" json:"args,omitempty"`
}

// PackageConfig encapsulates the configuration for artifact targets
type PackageConfig struct {
	// Signer is the configuration to use for signing packages
	Signer *PackageSigner `yaml:"signer,omitempty" json:"signer,omitempty"`
}

func (s *SystemdConfiguration) IsEmpty() bool {
	if s == nil {
		return true
	}

	if len(s.Units) == 0 {
		return true
	}

	return false
}

func (s *SystemdConfiguration) EnabledUnits() map[string]SystemdUnitConfig {
	if len(s.Units) == 0 {
		return nil
	}

	units := make(map[string]SystemdUnitConfig)
	for path, unit := range s.Units {
		if unit.Enable {
			units[path] = unit
		}
	}

	return units
}

type ExtDecodeConfig struct {
	AllowUnknownFields bool
}

var (
	ErrNodeNotFound  = errors.New("node not found")
	ErrInvalidExtKey = errors.New("extension keys must start with \"x-\"")
)

// Ext reads the extension field from the spec and unmarshals it into the target
// value.
func (s *Spec) Ext(key string, target interface{}, opts ...func(*ExtDecodeConfig)) error {
	v, ok := s.extensions[key]
	if !ok {
		return errors.Wrapf(ErrNodeNotFound, "extension field not found %q", key)
	}

	var yamlOpts []yaml.DecodeOption
	if len(opts) > 0 {
		var cfg ExtDecodeConfig
		for _, opt := range opts {
			opt(&cfg)
		}

		if !cfg.AllowUnknownFields {
			yamlOpts = append(yamlOpts, yaml.Strict())
		}
	}

	return yaml.NodeToValue(v, target, yamlOpts...)
}

// WithExtension adds an extension field to the spec.
// If the value is set to a []byte, it is used as-is and is expected to already
// be in YAML format.
func (s *Spec) WithExtension(key string, value interface{}) error {
	if !strings.HasPrefix(key, "x-") && !strings.HasPrefix(key, "X-") {
		return errors.Wrap(ErrInvalidExtKey, key)
	}

	if s.extensions == nil {
		s.extensions = make(extensionFields)
	}

	dt, ok := value.([]byte)
	if ok {
		parsed, err := parser.ParseBytes(dt, parseModeIgnoreComments)
		if err != nil {
			return errors.Wrap(err, "extension value provided is a []byte but is not valid YAML")
		}
		s.extensions[key] = parsed.Docs[0].Body
		return nil
	}

	node, err := yaml.ValueToNode(value)
	if err != nil {
		return errors.Wrapf(err, "failed to convert extension field %q to AST node", key)
	}
	s.extensions[key] = node
	return nil
}
