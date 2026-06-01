package deb

import (
	_ "embed"
	"fmt"
	"io"
	"maps"
	"strconv"
	"strings"
	"text/template"

	"github.com/project-dalec/dalec"
	"golang.org/x/exp/slices"
)

func WriteControl(spec *dalec.Spec, target string, w io.Writer) error {
	return controlTmpl.Execute(w, &controlWrapper{spec, target})
}

type controlWrapper struct {
	*dalec.Spec
	Target string
}

func (w *controlWrapper) Architecture() string {
	if w.NoArch {
		return "all"
	}
	return "linux-any"
}

// NOTE: This is very basic and does not handle things like grouped constraints
// Given this is just trying to shim things to allow either the rpm format or the deb format
// in its basic form, this is sufficient for now.
func formatVersionConstraint(v string) string {
	prefix, suffix, ok := strings.Cut(v, " ")
	if !ok {
		if len(prefix) >= 1 {
			_, err := strconv.Atoi(prefix[:1])
			if err == nil {
				// This is just a version number, assume it should use the equal symbol
				return "= " + v
			}
		}
		return v
	}

	switch prefix {
	case "<":
		return "<< " + suffix
	case ">":
		return ">> " + suffix
	case "==":
		return "= " + suffix
	default:
		return v
	}
}

// AppendConstraints takes an input list of packages and returns a new list of
// packages with the constraints appended for use in a debian/control file.
// The output list is sorted lexicographically.
func AppendConstraints(deps dalec.PackageDependencyList) []string {
	if deps == nil {
		return nil
	}
	out := dalec.SortMapKeys(deps)

	for i, dep := range out {
		constraints := deps[dep]
		var versionConstraints []string
		// Format is specified in https://www.debian.org/doc/debian-policy/ch-relationships.html#syntax-of-relationship-fields
		if len(constraints.Version) > 0 {
			ls := constraints.Version
			slices.Sort(ls)
			for _, v := range ls {
				versionConstraints = append(versionConstraints, fmt.Sprintf("%s (%s)", dep, formatVersionConstraint(v)))
			}
		} else {
			versionConstraints = append(versionConstraints, dep)
		}

		if len(constraints.Arch) > 0 {
			ls := constraints.Arch
			slices.Sort(ls)
			for j, vc := range versionConstraints {
				versionConstraints[j] = fmt.Sprintf("%s [%s]", vc, strings.Join(ls, " "))
			}
		}

		out[i] = strings.Join(versionConstraints, ", ")
	}

	return out
}

func (w *controlWrapper) depends(buf *strings.Builder, depsSpec *dalec.PackageDependencies) {
	// Add in deps vars that will get resolved by debbuild
	// In some cases these are not necessary (maybe even most), but when they are
	// it is important.
	// When not needed lintian may throw warnings but that's ok.
	// If these aren't actually needed they'll resolve to nothing and don't cause
	// any changes.
	const (
		// shlibs:Depends is a special variable that is replaced by the package
		// manager with the list of shared libraries that the package depends on.
		shlibsDeps = "${shlibs:Depends}"
		// misc:Depends is a special variable that is replaced by the package
		// manager with the list of miscellaneous dependencies that the package
		// depends on from debhelper programs that are to be invoked in a post-install script.
		miscDeps = "${misc:Depends}"
	)

	rtDeps := depsSpec.GetRuntime()
	needsClone := rtDeps != nil

	artifacts := w.Spec.GetArtifacts(w.Target)
	if !artifacts.DisableAutoRequires {
		if _, exists := rtDeps[shlibsDeps]; !exists {
			if needsClone {
				rtDeps = maps.Clone(rtDeps)
				needsClone = false
			}

			if rtDeps == nil {
				rtDeps = make(dalec.PackageDependencyList)
			}
			rtDeps[shlibsDeps] = dalec.PackageConstraints{}
		}
	}

	// We must add miscDeps regardless of `DisableAutoRequires` because
	// debhelper programs that are to be invoked in a post-install script
	// will not be able to function without it.
	if _, exists := rtDeps[miscDeps]; !exists {
		if needsClone {
			rtDeps = maps.Clone(rtDeps)
		}
		if rtDeps == nil {
			rtDeps = make(dalec.PackageDependencyList)
		}
		rtDeps[miscDeps] = dalec.PackageConstraints{}
	}

	deps := AppendConstraints(rtDeps)
	fmt.Fprintln(buf, multiline("Depends", deps))
}

// multiline attempts to format a field with multiple values in a way that is more human readable
// with line breaks and indentation.
func multiline(field string, values []string) string {
	return fmt.Sprintf("%s: %s", field, strings.Join(values, ",\n"+strings.Repeat(" ", len(field)+2)))
}

func (w *controlWrapper) recommends(buf *strings.Builder, depsSpec *dalec.PackageDependencies) {
	if depsSpec == nil || len(depsSpec.Recommends) == 0 {
		return
	}

	deps := AppendConstraints(depsSpec.Recommends)
	fmt.Fprintln(buf, multiline("Recommends", deps))
}

func (w *controlWrapper) BuildDeps() fmt.Stringer {
	b := &strings.Builder{}

	specDeps := w.Spec.GetPackageDeps(w.Target).GetBuild()
	deps := AppendConstraints(specDeps)

	deps = append(deps, fmt.Sprintf("debhelper-compat (= %s)", DebHelperCompat))

	fmt.Fprintln(b, multiline("Build-Depends", deps))
	return b
}

func (w *controlWrapper) AllRuntimeDeps() fmt.Stringer {
	b := &strings.Builder{}

	deps := w.Spec.GetPackageDeps(w.Target)
	w.depends(b, deps)
	w.recommends(b, deps)

	return b
}

func (w *controlWrapper) Replaces() fmt.Stringer {
	b := &strings.Builder{}
	replaces := w.Spec.GetReplaces(w.Target)
	if len(replaces) == 0 {
		return b
	}

	ls := AppendConstraints(replaces)

	fmt.Fprintln(b, multiline("Replaces", ls))
	return b
}

func (w *controlWrapper) Conflicts() fmt.Stringer {
	b := &strings.Builder{}
	conflicts := w.Spec.GetConflicts(w.Target)
	if len(conflicts) == 0 {
		return b
	}

	ls := AppendConstraints(conflicts)
	fmt.Fprintln(b, multiline("Conflicts", ls))
	return b
}

func (w *controlWrapper) Provides() fmt.Stringer {
	b := &strings.Builder{}
	provides := w.Spec.GetProvides(w.Target)
	if len(provides) == 0 {
		return b
	}

	ls := AppendConstraints(provides)
	fmt.Fprintln(b, multiline("Provides", ls))
	return b
}

var (
	//go:embed templates/debian_control.tmpl
	controlTmplContent []byte

	controlTmpl = template.Must(template.New("control").Parse(string(controlTmplContent)))
)
