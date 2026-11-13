package dalec

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/moby/buildkit/frontend/dockerfile/shell"
)

// NamedImage configures a target-local container independently of the default image.
type NamedImage struct {
	ImageConfig `yaml:",inline"`
	// Packages selects resolved package names. Omit to install all built packages.
	Packages []string `yaml:"packages,omitempty" json:"packages,omitempty"`
	// Tests are appended to root and target tests for this image.
	Tests []*TestSpec `yaml:"tests,omitempty" json:"tests,omitempty"`
}

func (i *NamedImage) validate() error {
	var errs []error
	if err := i.ImageConfig.validate(); err != nil {
		errs = append(errs, err)
	}
	if i.Packages != nil && len(i.Packages) == 0 {
		errs = append(errs, fmt.Errorf("packages must not be empty; omit packages to install all"))
	}
	for _, test := range i.Tests {
		if err := test.validate(); err != nil {
			errs = append(errs, fmt.Errorf("test %s: %w", test.Name, err))
		}
	}
	return errors.Join(errs...)
}

func (i *NamedImage) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(string) bool) error {
	var errs []error
	if err := i.ImageConfig.processBuildArgs(lex, args, allowArg); err != nil {
		errs = append(errs, err)
	}
	for _, test := range i.Tests {
		if err := test.processBuildArgs(lex, args, allowArg); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Spec) validateNamedImages(targetKey string) error {
	target := s.Targets[targetKey]
	names := map[string]bool{s.Name: true}
	for key, pkg := range target.Packages {
		names[pkg.ResolvedName(s.Name, key)] = true
	}
	var errs []error
	for name, img := range SortedMapIter(target.Images) {
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
			errs = append(errs, fmt.Errorf("target %s: invalid image name %q", targetKey, name))
		}
		for _, pkg := range img.Packages {
			if !names[pkg] {
				errs = append(errs, fmt.Errorf("target %s: image %q: unknown package %q", targetKey, name, pkg))
			}
		}
	}
	return errors.Join(errs...)
}

// WithNamedImage returns a container-only view of the spec without modifying
// package build inputs or inheriting default image configuration.
func (s *Spec) WithNamedImage(targetKey, name string) (*Spec, error) {
	if name == "" {
		return s, nil
	}
	img, ok := s.Targets[targetKey].Images[name]
	if !ok {
		return nil, fmt.Errorf("target %s: unknown image %q", targetKey, name)
	}
	out := *s
	out.Targets = maps.Clone(s.Targets)
	target := out.Targets[targetKey]
	target.Image = nil
	target.Tests = append(slices.Clone(target.Tests), img.Tests...)
	out.Targets[targetKey] = target
	cfg := img.ImageConfig
	out.Image = &cfg
	out.imagePackages = img.Packages
	return &out, nil
}

// GetImagePackages returns the explicitly selected packages, or nil for install-all.
func (s *Spec) GetImagePackages() []string {
	return slices.Clone(s.imagePackages)
}
