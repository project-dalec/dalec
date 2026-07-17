package windows

import (
	goerrors "errors"
	"fmt"

	"github.com/project-dalec/dalec"
)

func validateRuntimeDeps(s *dalec.Spec, targetKey string) error {
	var errs []error
	rd := s.GetPackageDeps(targetKey).GetRuntime()
	if len(rd) != 0 {
		errs = append(errs, fmt.Errorf("package %q: targets with windows output images cannot have runtime dependencies", s.Name))
	}

	subpackages := s.GetSubPackages(targetKey)
	for _, key := range dalec.SortMapKeys(subpackages) {
		pkg := subpackages[key]
		if len(pkg.Dependencies.GetRuntime()) == 0 {
			continue
		}
		errs = append(errs, fmt.Errorf("package %q: targets with windows output images cannot have runtime dependencies", pkg.ResolvedName(s.Name, key)))
	}

	return goerrors.Join(errs...)
}

// validateZipArtifacts ensures every package produced for a windowscross/zip
// target ships at least one artifact. A zip file must contain something, so an
// empty primary or supplemental package is rejected.
func validateZipArtifacts(spec *dalec.Spec, targetKey string) error {
	var errs []error
	for _, pkg := range windowsPackages(spec, targetKey) {
		if len(pkg.Binaries) == 0 {
			errs = append(errs, fmt.Errorf("package %q produces no artifacts; windowscross/zip requires at least one binary per package", pkg.Name))
		}
	}
	return goerrors.Join(errs...)
}
