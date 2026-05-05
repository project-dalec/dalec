package dalec

import (
	goerrors "errors"

	"github.com/moby/buildkit/frontend/dockerfile/shell"
	"github.com/pkg/errors"
)

const (
	// PreBuiltPkgSuffix is what is expected to be appended to a targetKey when it's
	// meant to be a target distro specific package (e.g. azlinux3-pkg,
	// windowscross-pkg, bookworm-pkg, etc.). When this is provided and used to buildkit
	// and container, it will take precedence over GenericPkg.
	PreBuiltPkgSuffix = "-pkg"
	// If not a target specific package, but we want to indicate the use of a
	// prebuilt package, we use GenericPkg to indicate that it's not target specific.
	GenericPkg = "pkg"
)

// Target defines a distro-specific build target.
// This is used in [Spec] to specify the build target for a distro.
type Target struct {
	// Dependencies are the different dependencies that need to be specified in the package.
	Dependencies *PackageDependencies `yaml:"dependencies,omitempty" json:"dependencies,omitempty"`

	// Image is the image configuration when the target output is a container image.
	Image *ImageConfig `yaml:"image,omitempty" json:"image,omitempty"`

	// Frontend is the frontend configuration to use for the target.
	// This is used to forward the build to a different, dalec-compatible frontend.
	// This can be useful when testing out new distros or using a different version of the frontend for a given distro.
	Frontend *Frontend `yaml:"frontend,omitempty" json:"frontend,omitempty"`

	// Tests are the list of tests to run which are specific to the target.
	// Tests are appended to the list of tests in the main [Spec]
	Tests []*TestSpec `yaml:"tests,omitempty" json:"tests,omitempty"`

	// PackageConfig is the configuration to use for artifact targets, such as
	// rpms, debs, or zip files containing Windows binaries
	PackageConfig *PackageConfig `yaml:"package_config,omitempty" json:"package_config,omitempty"`

	// Artifacts describes all of the artifact configurations to include for this specific target.
	Artifacts *Artifacts `yaml:"artifacts,omitempty" json:"artifacts,omitempty"`

	// Provides is the list of packages that this target provides.
	Provides PackageDependencyList `yaml:"provides,omitempty" json:"provides,omitempty"`

	// Replaces is the list of packages that this target replaces/obsoletes.
	Replaces PackageDependencyList `yaml:"replaces,omitempty" json:"replaces,omitempty"`

	// Conflicts is the list of packages that this target conflicts with.
	Conflicts PackageDependencyList `yaml:"conflicts,omitempty" json:"conflicts,omitempty"`

	// Packages defines supplemental packages produced from the same build output
	// as the primary package. Each entry produces an additional package with its
	// own artifact selection, runtime dependencies, and metadata.
	// The map key is used as a suffix for the default package name ("<specName>-<key>").
	Packages map[string]SubPackage `yaml:"packages,omitempty" json:"packages,omitempty"`
}

func (t *Target) validate() error {
	var errs []error
	if err := t.Dependencies.validate(); err != nil {
		errs = append(errs, errors.Wrap(err, "dependencies"))
	}

	if err := t.Image.validate(); err != nil {
		errs = append(errs, errors.Wrap(err, "image"))
	}

	for _, test := range t.Tests {
		if err := test.validate(); err != nil {
			errs = append(errs, errors.Wrapf(err, "test %s", test.Name))
		}
	}

	if err := t.Image.validate(); err != nil {
		errs = append(errs, errors.Wrap(err, "postinstall"))
	}

	for key, pkg := range t.Packages {
		if err := pkg.validate(); err != nil {
			errs = append(errs, errors.Wrapf(err, "package %s", key))
		}
	}

	return goerrors.Join(errs...)
}

func (t *Target) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(string) bool) error {
	var errs []error
	for _, tt := range t.Tests {
		if err := tt.processBuildArgs(lex, args, allowArg); err != nil {
			errs = append(errs, err)
		}
	}

	if t.PackageConfig != nil {
		if err := t.PackageConfig.processBuildArgs(lex, args, allowArg); err != nil {
			errs = append(errs, errors.Wrap(err, "package config"))
		}
	}

	if err := t.Image.processBuildArgs(lex, args, allowArg); err != nil {
		errs = append(errs, errors.Wrap(err, "package config"))
	}

	if err := t.Dependencies.processBuildArgs(lex, args, allowArg); err != nil {
		errs = append(errs, errors.Wrap(err, "dependencies"))
	}

	for k, v := range t.Provides {
		for i, ver := range v.Version {
			updated, err := expandArgs(lex, ver, args, allowArg)
			if err != nil {
				errs = append(errs, errors.Wrapf(err, "provides %s version %d", k, i))
				continue
			}
			t.Provides[k].Version[i] = updated
		}
	}

	for k, v := range t.Replaces {
		for i, ver := range v.Version {
			updated, err := expandArgs(lex, ver, args, allowArg)
			if err != nil {
				errs = append(errs, errors.Wrapf(err, "replaces %s version %d", k, i))
				continue
			}
			t.Replaces[k].Version[i] = updated
		}
	}

	for k, v := range t.Conflicts {
		for i, ver := range v.Version {
			updated, err := expandArgs(lex, ver, args, allowArg)
			if err != nil {
				errs = append(errs, errors.Wrapf(err, "conflicts %s version %d", k, i))
				continue
			}
			t.Conflicts[k].Version[i] = updated
		}
	}

	for key, pkg := range t.Packages {
		if err := pkg.processBuildArgs(lex, args, allowArg); err != nil {
			errs = append(errs, errors.Wrapf(err, "package %s", key))
		}
		t.Packages[key] = pkg
	}

	return goerrors.Join(errs...)
}

func (t *Target) fillDefaults() {
	t.Dependencies.fillDefaults()
	t.Image.fillDefaults()

	for key, pkg := range t.Packages {
		pkg.fillDefaults()
		t.Packages[key] = pkg
	}
}
