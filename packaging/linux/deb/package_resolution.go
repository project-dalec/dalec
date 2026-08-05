package deb

import "github.com/project-dalec/dalec"

type resolvedPackage struct {
	name                string
	description         string
	artifacts           dalec.Artifacts
	runtimeDependencies dalec.PackageDependencyList
	recommends          dalec.PackageDependencyList
	replaces            dalec.PackageDependencyList
	conflicts           dalec.PackageDependencyList
	provides            dalec.PackageDependencyList
	primary             bool
}

func resolvePackages(spec *dalec.Spec, target string) []resolvedPackage {
	resolved := []resolvedPackage{resolvePrimaryPackage(spec, target)}

	for key, pkg := range dalec.GetSubPackagesForTarget(spec, target) {
		var artifacts dalec.Artifacts
		if pkg.Artifacts != nil {
			artifacts = *pkg.Artifacts
		}

		resolved = append(resolved, resolvedPackage{
			name:                pkg.ResolvedName(spec.Name, key),
			description:         pkg.Description,
			artifacts:           artifacts,
			runtimeDependencies: pkg.Dependencies.GetRuntime(),
			recommends:          pkg.Dependencies.GetRecommends(),
			replaces:            pkg.Replaces,
			conflicts:           pkg.Conflicts,
			provides:            pkg.Provides,
		})
	}

	return resolved
}

func resolvePrimaryPackage(spec *dalec.Spec, target string) resolvedPackage {
	artifacts := spec.GetArtifacts(target)
	dependencies := spec.GetPackageDeps(target)

	return resolvedPackage{
		name:                spec.Name,
		description:         spec.Description,
		artifacts:           artifacts,
		runtimeDependencies: dependencies.GetRuntime(),
		recommends:          dependencies.GetRecommends(),
		replaces:            spec.GetReplaces(target),
		conflicts:           spec.GetConflicts(target),
		provides:            spec.GetProvides(target),
		primary:             true,
	}
}
