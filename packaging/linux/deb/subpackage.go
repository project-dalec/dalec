package deb

import "github.com/project-dalec/dalec"

type resolvedSubPackage struct {
	name string
	pkg  dalec.SubPackage
}

func resolveSubPackages(spec *dalec.Spec, target string) []resolvedSubPackage {
	var resolved []resolvedSubPackage

	for key, pkg := range dalec.GetSubPackagesForTarget(spec, target) {
		resolved = append(resolved, resolvedSubPackage{
			name: pkg.ResolvedName(spec.Name, key),
			pkg:  pkg,
		})
	}

	return resolved
}
