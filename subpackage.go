package dalec

import (
	goerrors "errors"
	"fmt"

	"github.com/moby/buildkit/frontend/dockerfile/shell"
	"github.com/pkg/errors"
)

// SubPackageDependencies contains only the dependency fields valid for
// supplemental packages. Build dependencies are shared with the primary
// package and cannot be overridden.
type SubPackageDependencies struct {
	// Runtime is the list of packages required to install/run the supplemental package.
	Runtime PackageDependencyList `yaml:"runtime,omitempty" json:"runtime,omitempty"`
	// Recommends is the list of packages recommended to install with the supplemental package.
	// Note: Not all package managers support this (e.g. rpm)
	Recommends PackageDependencyList `yaml:"recommends,omitempty" json:"recommends,omitempty"`
}

func (d *SubPackageDependencies) GetRuntime() PackageDependencyList {
	if d == nil {
		return nil
	}
	return d.Runtime
}

func (d *SubPackageDependencies) GetRecommends() PackageDependencyList {
	if d == nil {
		return nil
	}
	return d.Recommends
}

func (d *SubPackageDependencies) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(string) bool) error {
	if d == nil {
		return nil
	}

	var errs []error
	for k, v := range d.Runtime {
		for i, ver := range v.Version {
			updated, err := expandArgs(lex, ver, args, allowArg)
			if err != nil {
				errs = append(errs, errors.Wrapf(err, "runtime version %s", ver))
				continue
			}
			v.Version[i] = updated
		}
		d.Runtime[k] = v
	}

	for k, v := range d.Recommends {
		for i, ver := range v.Version {
			updated, err := expandArgs(lex, ver, args, allowArg)
			if err != nil {
				errs = append(errs, errors.Wrapf(err, "recommends version %s", ver))
				continue
			}
			v.Version[i] = updated
		}
		d.Recommends[k] = v
	}

	return goerrors.Join(errs...)
}

// SubPackage defines a supplemental package produced from the same build
// output as the primary package. Each supplemental package has its own
// artifact selection, runtime dependencies, and package metadata.
//
// Supplemental packages share the primary package's build steps, sources,
// version, revision, license, vendor, and website. They cannot override
// these fields.
type SubPackage struct {
	// Name overrides the default package name.
	// By default, the package name is "<parent>-<key>" where <key> is the map
	// key under which this SubPackage is defined. Set this to use a fully custom
	// package name instead.
	Name string `yaml:"name,omitempty" json:"name,omitempty"`

	// Description is the package description. This is required — both RPM and
	// Debian require a description/summary for every subpackage.
	Description string `yaml:"description" json:"description" jsonschema:"required"`

	// Artifacts specifies which build outputs go into this supplemental package.
	// This is self-contained — no artifacts are inherited from the primary package.
	Artifacts *Artifacts `yaml:"artifacts,omitempty" json:"artifacts,omitempty"`

	// Dependencies specifies runtime dependencies for this supplemental package.
	// Only runtime and recommends dependencies are allowed; build dependencies
	// are shared with the primary package.
	Dependencies *SubPackageDependencies `yaml:"dependencies,omitempty" json:"dependencies,omitempty"`

	// Conflicts is the list of packages that conflict with this supplemental package.
	Conflicts PackageDependencyList `yaml:"conflicts,omitempty" json:"conflicts,omitempty"`

	// Provides is the list of things this supplemental package provides.
	Provides PackageDependencyList `yaml:"provides,omitempty" json:"provides,omitempty"`

	// Replaces is the list of packages that this supplemental package replaces/obsoletes.
	Replaces PackageDependencyList `yaml:"replaces,omitempty" json:"replaces,omitempty"`
}

// ResolvedName returns the package name that this SubPackage will produce.
// If [SubPackage.Name] is set, it is returned as-is.
// Otherwise, the name is "<parentName>-<mapKey>".
func (s *SubPackage) ResolvedName(parentName, mapKey string) string {
	if s.Name != "" {
		return s.Name
	}
	return parentName + "-" + mapKey
}

func (s *SubPackage) validate() error {
	var errs []error

	if s.Description == "" {
		errs = append(errs, fmt.Errorf("description is required"))
	}

	if s.Artifacts != nil {
		if err := s.Artifacts.validate(); err != nil {
			errs = append(errs, errors.Wrap(err, "artifacts"))
		}
	}

	return goerrors.Join(errs...)
}

func (s *SubPackage) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(string) bool) error {
	var errs []error

	if s.Name != "" {
		updated, err := expandArgs(lex, s.Name, args, allowArg)
		if err != nil {
			errs = append(errs, errors.Wrap(err, "name"))
		} else {
			s.Name = updated
		}
	}

	if s.Description != "" {
		updated, err := expandArgs(lex, s.Description, args, allowArg)
		if err != nil {
			errs = append(errs, errors.Wrap(err, "description"))
		} else {
			s.Description = updated
		}
	}

	if err := s.Dependencies.processBuildArgs(lex, args, allowArg); err != nil {
		errs = append(errs, errors.Wrap(err, "dependencies"))
	}

	for k, v := range s.Conflicts {
		for i, ver := range v.Version {
			updated, err := expandArgs(lex, ver, args, allowArg)
			if err != nil {
				errs = append(errs, errors.Wrapf(err, "conflicts %s version %d", k, i))
				continue
			}
			s.Conflicts[k].Version[i] = updated
		}
	}

	for k, v := range s.Provides {
		for i, ver := range v.Version {
			updated, err := expandArgs(lex, ver, args, allowArg)
			if err != nil {
				errs = append(errs, errors.Wrapf(err, "provides %s version %d", k, i))
				continue
			}
			s.Provides[k].Version[i] = updated
		}
	}

	for k, v := range s.Replaces {
		for i, ver := range v.Version {
			updated, err := expandArgs(lex, ver, args, allowArg)
			if err != nil {
				errs = append(errs, errors.Wrapf(err, "replaces %s version %d", k, i))
				continue
			}
			s.Replaces[k].Version[i] = updated
		}
	}

	return goerrors.Join(errs...)
}

func (s *SubPackage) fillDefaults() {
}

// validateSubPackageNames checks that no two supplemental packages in the same
// target resolve to the same name, and that no supplemental package name
// conflicts with the primary package name.
func validateSubPackageNames(specName, targetName string, packages map[string]SubPackage) error {
	if len(packages) == 0 {
		return nil
	}

	var errs []error
	seen := make(map[string]string, len(packages)) // resolved name → map key

	for key, pkg := range packages {
		resolved := pkg.ResolvedName(specName, key)

		if resolved == specName {
			errs = append(errs, fmt.Errorf("target %s: package %q resolves to name %q which conflicts with the primary package name", targetName, key, resolved))
		}

		if prevKey, exists := seen[resolved]; exists {
			errs = append(errs, fmt.Errorf("target %s: packages %q and %q both resolve to the same name %q", targetName, prevKey, key, resolved))
		}
		seen[resolved] = key
	}

	return goerrors.Join(errs...)
}

// GetSubPackages returns the supplemental packages defined for the given target.
// Returns nil if the target does not exist or has no supplemental packages.
func (s *Spec) GetSubPackages(targetKey string) map[string]SubPackage {
	t, ok := s.Targets[targetKey]
	if !ok {
		return nil
	}
	return t.Packages
}

// GetAllPackageNames returns the resolved names of all packages (primary +
// supplemental) for the given target. The primary package name is always first.
func (s *Spec) GetAllPackageNames(targetKey string) []string {
	names := []string{s.Name}
	for key, pkg := range s.GetSubPackages(targetKey) {
		names = append(names, pkg.ResolvedName(s.Name, key))
	}
	return names
}
