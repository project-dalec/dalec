package dalec

import (
	"context"
	goerrors "errors"
	"slices"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
	"github.com/pkg/errors"
)

// PackageConstraints is used to specify complex constraints for a package dependency.
type PackageConstraints struct {
	// Version is a list of version constraints for the package.
	// The format of these strings is dependent on the package manager of the target system.
	// Examples:
	//   [">=1.0.0", "<2.0.0"]
	Version []string `yaml:"version,omitempty" json:"version,omitempty"`
	// Arch is a list of architecture constraints for the package.
	// Use this to specify that a package constraint only applies to certain architectures.
	Arch []string `yaml:"arch,omitempty" json:"arch,omitempty"`

	// _sourceMap holds the source map for this specific package entry. It is
	// populated by the YAML source-map attachment code so callers can attribute
	// errors directly to the package entry line in the YAML.
	_sourceMap *sourceMap `json:"-" yaml:"-"`
}

// GetSourceLocation returns an llb.ConstraintsOpt for this package's source map or nil when not present.
func (pc *PackageConstraints) GetSourceLocation(state llb.State) llb.ConstraintsOpt {
	if pc == nil {
		return ConstraintsOptFunc(func(c *llb.Constraints) {})
	}
	return pc._sourceMap.GetLocation(state)
}

type PackageDependencyList map[string]PackageConstraints

func (pc *PackageConstraints) UnmarshalYAML(ctx context.Context, node ast.Node) error {
	type internal PackageConstraints
	var i internal

	if node.Type() != ast.NullType {
		if err := yaml.NodeToValue(node, &i, decodeOpts(ctx)...); err != nil {
			return errors.Wrap(err, "unmarshal package constraints")
		}
	}

	*pc = PackageConstraints(i)
	pc._sourceMap = newSourceMap(ctx, node)
	return nil
}

func (l *PackageDependencyList) UnmarshalYAML(ctx context.Context, node ast.Node) error {
	mapNode, ok := node.(*ast.MappingNode)
	if !ok {
		if node.Type() != ast.SequenceType {
			// pass this through the yaml decoder so it can generate a useful error
			type internal PackageDependencyList
			var i internal
			return yaml.NodeToValue(node, &i)
		}

		// Most likely this is due to a spec using the old list format.
		// Upgrade the list to to the new format.
		var ls []sourceMappedValue[string]

		dec := getDecoder(ctx)

		err := dec.DecodeFromNodeContext(ctx, node, &ls)
		if err != nil {
			return errors.Wrap(err, "error unmarshalling package dependency list")
		}

		if len(ls) == 0 {
			return nil
		}

		result := make(PackageDependencyList, len(ls))
		for _, v := range ls {
			result[v.Value] = PackageConstraints{
				_sourceMap: v.sourceMap,
			}
		}

		return nil
	}

	result := make(PackageDependencyList)
	dec := getDecoder(ctx)
	for _, v := range mapNode.Values {
		var pc PackageConstraints
		if err := dec.DecodeFromNodeContext(ctx, v.Value, &pc); err != nil {
			return errors.Wrap(err, "unmarshal package constraints")
		}

		key, ok := v.Key.(*ast.StringNode)
		if !ok {
			return goerrors.New("expected string key for package dependency")
		}

		// Handle case where a package constraint is specified:
		// e.g.
		//   pkg-name:
		//	 	version: [">=1.0.0"]
		// In this case, the the line number would be pointing at the line below `pkg-name`
		// However, we want to include the package name itself in the source map.
		if pc._sourceMap.pos.Start.Line > int32(key.GetToken().Position.Line) {
			pc._sourceMap.pos.Start.Line = int32(key.GetToken().Position.Line)
		}
		result[key.Value] = pc
	}

	*l = result

	return nil
}

func (l PackageDependencyList) GetSourceLocation(state llb.State) llb.ConstraintsOpt {
	var locations []llb.ConstraintsOpt
	for _, pc := range l {
		if c := pc.GetSourceLocation(state); c != nil {
			locations = append(locations, c)
		}
	}
	return MergeSourceLocations(locations...)
}

// PackageDependencies is a list of dependencies for a package.
// This will be included in the package metadata so that the package manager can install the dependencies.
// It also includes build-time dedendencies, which we'll install before running any build steps.
type PackageDependencies struct {
	// Build is the list of packagese required to build the package.
	Build PackageDependencyList `yaml:"build,omitempty" json:"build,omitempty"`
	// Runtime is the list of packages required to install/run the package.
	Runtime PackageDependencyList `yaml:"runtime,omitempty" json:"runtime,omitempty"`
	// Recommends is the list of packages recommended to install with the generated package.
	// Note: Not all package managers support this (e.g. rpm)
	Recommends PackageDependencyList `yaml:"recommends,omitempty" json:"recommends,omitempty"`
	// Sysext is the list of packages to include in the generated system
	// extension. No dependency resolution is performed when generating system
	// extensions, so all required dependencies must be explicitly listed here.
	Sysext PackageDependencyList `yaml:"sysext,omitempty" json:"sysext,omitempty"`

	// Test lists any extra packages required for running tests
	// These packages are only installed for tests which have steps that require
	// running a command in the built container.
	// Use a map so test dependencies can have PackageConstraints (and source maps).
	Test PackageDependencyList `yaml:"test,omitempty" json:"test,omitempty"`

	// ExtraRepos is used to inject extra package repositories that may be used to
	// satisfy package dependencies in various stages.
	ExtraRepos []PackageRepositoryConfig `yaml:"extra_repos,omitempty" json:"extra_repos,omitempty"`
}

func (p *PackageDependencies) GetBuild() PackageDependencyList {
	if p == nil {
		return nil
	}
	return p.Build
}

func (p *PackageDependencies) GetRuntime() PackageDependencyList {
	if p == nil {
		return nil
	}
	return p.Runtime
}

func (p *PackageDependencies) GetRecommends() PackageDependencyList {
	if p == nil {
		return nil
	}
	return p.Recommends
}

func (p *PackageDependencies) GetSysext() PackageDependencyList {
	if p == nil {
		return nil
	}
	return p.Sysext
}

func (p *PackageDependencies) GetTest() PackageDependencyList {
	if p == nil {
		return nil
	}
	return p.Test
}

// PackageRepositoryConfig
type PackageRepositoryConfig struct {
	// Keys are the list of keys that need to be imported to use the configured
	// repositories
	Keys map[string]Source `yaml:"keys,omitempty" json:"keys,omitempty"`

	// Config list of repo configs to to add to the environment.  The format of
	// these configs are distro specific (e.g. apt/yum configs).
	Config map[string]Source `yaml:"config" json:"config"`

	// Data lists all the extra data that needs to be made available for the
	// provided repository config to work.
	// As an example, if the provided config is referencing a file backed repository
	// then data would include the file data, assuming its not already available
	// in the environment.
	Data []SourceMount `yaml:"data,omitempty" json:"data,omitempty"`
	// Envs specifies the list of environments to make the repositories available
	// during.
	// Acceptable values are:
	//  - "build"   - Repositories are added prior to installing build dependencies
	//  - "test"    - Repositories are added prior to installing test dependencies
	//  - "install" - Repositories are added prior to installing the output
	//                package in a container build target.
	Envs []string `yaml:"envs" json:"envs" jsonschema:"enum=build,enum=test,enum=install"`
}

func (d *PackageDependencies) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(string) bool) error {
	if d == nil {
		return nil
	}

	for k, v := range d.Build {
		for i, ver := range v.Version {
			updated, err := expandArgs(lex, ver, args, allowArg)
			if err != nil {
				return errors.Wrapf(err, "build version %s", ver)
			}
			v.Version[i] = updated
		}
		d.Build[k] = v
	}

	for k, v := range d.Runtime {
		for i, ver := range v.Version {
			updated, err := expandArgs(lex, ver, args, allowArg)
			if err != nil {
				return errors.Wrapf(err, "runtime version %s", ver)
			}
			v.Version[i] = updated
		}
		d.Runtime[k] = v
	}

	var errs []error
	for i, repo := range d.ExtraRepos {
		if err := repo.processBuildArgs(lex, args, allowArg); err != nil {
			errs = append(errs, errors.Wrapf(err, "extra repos index %d", i))
		}
		d.ExtraRepos[i] = repo
	}
	return goerrors.Join(errs...)
}

func (r *PackageRepositoryConfig) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(string) bool) error {
	if r == nil {
		return nil
	}

	var errs []error

	for k := range r.Config {
		src := r.Config[k]
		if err := src.processBuildArgs(lex, args, allowArg); err != nil {
			errs = append(errs, errors.Wrapf(err, "config %s", k))
			continue
		}
		r.Config[k] = src
	}

	for k := range r.Keys {
		src := r.Keys[k]
		if err := src.processBuildArgs(lex, args, allowArg); err != nil {
			errs = append(errs, errors.Wrapf(err, "key %s", k))
			continue
		}
		r.Keys[k] = src
	}

	for i := range r.Data {
		d := r.Data[i]
		if err := d.processBuildArgs(lex, args, allowArg); err != nil {
			errs = append(errs, errors.Wrapf(err, "data index %d", i))
			continue
		}
		r.Data[i] = d
	}

	return goerrors.Join(errs...)
}

func (d *PackageDependencies) fillDefaults() {
	if d == nil {
		return
	}

	for i, r := range d.ExtraRepos {
		r.fillDefaults()
		d.ExtraRepos[i] = r
	}
}

func (r *PackageRepositoryConfig) fillDefaults() {
	if len(r.Envs) == 0 {
		// default to all stages for the extra repo if unspecified
		r.Envs = []string{"build", "install", "test"}
	}

	for i, src := range r.Config {
		s := &src
		s.fillDefaults()
		r.Config[i] = *s
	}

	for i, src := range r.Keys {
		s := &src
		s.fillDefaults()

		// Default to 0644 permissions for gpg keys. This is because apt will only import
		// keys with a particular permission set.
		if s.HTTP != nil {
			s.HTTP.Permissions = 0644
		}
		r.Keys[i] = *s
	}

	for i, mount := range r.Data {
		mount.fillDefaults(nil)
		r.Data[i] = mount
	}
}

func (d *PackageDependencies) validate() error {
	if d == nil {
		return nil
	}

	var errs []error
	for i, r := range d.ExtraRepos {
		if err := r.validate(); err != nil {
			errs = append(errs, errors.Wrapf(err, "extra repo %d", i))
		}
	}

	return goerrors.Join(errs...)
}

func (r *PackageRepositoryConfig) validate() error {
	var errs []error
	for name, src := range r.Keys {
		if err := src.validate(); err != nil {
			errs = append(errs, errors.Wrapf(err, "key %s", name))
		}
	}
	for name, src := range r.Config {
		if err := src.validate(); err != nil {
			errs = append(errs, errors.Wrapf(err, "config %s", name))
		}
	}
	for _, mnt := range r.Data {
		if err := mnt.validate(); err != nil {
			errs = append(errs, errors.Wrapf(err, "data mount path %s", mnt.Dest))
		}
	}

	return goerrors.Join(errs...)
}

func (p *PackageDependencies) GetExtraRepos(env string) []PackageRepositoryConfig {
	if p == nil {
		return nil
	}
	return GetExtraRepos(p.ExtraRepos, env)
}

func GetExtraRepos(repos []PackageRepositoryConfig, env string) []PackageRepositoryConfig {
	var out []PackageRepositoryConfig
	for _, repo := range repos {
		if slices.Contains(repo.Envs, env) {
			out = append(out, repo)
		}
	}
	return out
}

func (s *Spec) GetBuildRepos(targetKey string) []PackageRepositoryConfig {
	deps := s.GetPackageDeps(targetKey)
	return deps.GetExtraRepos("build")
}

func (s *Spec) GetInstallRepos(targetKey string) []PackageRepositoryConfig {
	deps := s.GetPackageDeps(targetKey)
	return deps.GetExtraRepos("install")
}

func (s *Spec) GetTestRepos(targetKey string) []PackageRepositoryConfig {
	deps := s.GetPackageDeps(targetKey)
	return deps.GetExtraRepos("test")
}
