// Package rpm provides RPM package building functionality for dalec.
package rpm

import (
	"fmt"
	"strings"

	"github.com/project-dalec/dalec"
	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"
)

func buildScriptSourceState(spec *dalec.Spec) *llb.State {
	if len(spec.Build.Steps) == 0 {
		return nil
	}

	script := buildScript(spec)
	st := llb.Scratch().File(llb.Mkfile("build.sh", 0755, []byte(script)))
	return &st
}

func buildScript(spec *dalec.Spec) string {
	b := &strings.Builder{}

	t := spec.Build
	if len(t.Steps) == 0 {
		return ""
	}

	fmt.Fprintln(b, "#!/bin/sh")
	fmt.Fprintln(b, "set -e")

	if spec.HasGomods() {
		// Older go versions did not have support for the `GOMODCACHE` var
		// This is a hack to try and make the build work by linking the go modules
		// we've already fetched into to module dir under $GOPATH
		// The default GOMODCACHE value is ${GOPATH}/pkg/mod.
		fmt.Fprintf(b, `test -n "$(go env GOMODCACHE)" || (GOPATH="$(go env GOPATH)"; mkdir -p "${GOPATH}/pkg" && ln -s "$(pwd)/%s" "${GOPATH}/pkg/mod")`, gomodsName)
		// Above command does not have a newline due to quoting issues, so add that here.
		fmt.Fprint(b, "\n")

		fmt.Fprintln(b, "export GOMODCACHE=\"$(pwd)/"+gomodsName+"\"")
		fmt.Fprintln(b, "export GOPROXY=\"file://$(pwd)/"+gomodsName+"/cache/download\"")
		fmt.Fprintln(b, "export GOSUMDB=off")
	}

	if spec.HasCargohomes() {
		// Set CARGO_HOME to point to our prepared cargo cache
		fmt.Fprintln(b, "export CARGO_HOME=\"$(pwd)/"+cargohomeName+"\"")
	}

	if spec.HasPips() {
		// Set up pip environment and install dependencies during build
		fmt.Fprintln(b, "# Set up pip environment")
		fmt.Fprintln(b, "export PIP_CACHE_DIR=\"$(pwd)/"+pipDepsName+"\"")
		fmt.Fprintln(b, "")
		fmt.Fprintln(b, "# Install pip dependencies from cache")
		fmt.Fprintln(b, "for reqfile in $(find . -name 'requirements*.txt' -o -name 'pyproject.toml' -o -name 'setup.py'); do")
		fmt.Fprintln(b, "  if [ -f \"$reqfile\" ]; then")
		fmt.Fprintln(b, "    reqdir=$(dirname \"$reqfile\")")
		fmt.Fprintln(b, "    mkdir -p \"$reqdir/site-packages\"")
		fmt.Fprintln(b, "    case \"$reqfile\" in")
		fmt.Fprintln(b, "      *.txt) python3 -m pip install --target=\"$reqdir/site-packages\" --find-links=\"${PIP_CACHE_DIR}\" --no-index --requirement=\"$reqfile\" 2>/dev/null || python3 -m pip install --target=\"$reqdir/site-packages\" --find-links=\"${PIP_CACHE_DIR}\" --no-index --requirement=\"$reqfile\" ;;")
		fmt.Fprintln(b, "      *) (cd \"$reqdir\" && python3 -m pip install --target=site-packages --find-links=\"${PIP_CACHE_DIR}\" --no-index . 2>/dev/null || python3 -m pip install --target=site-packages --find-links=\"${PIP_CACHE_DIR}\" --no-index .) ;;")
		fmt.Fprintln(b, "    esac")
		fmt.Fprintln(b, "    export PYTHONPATH=\"$reqdir/site-packages:${PYTHONPATH}\"")
		fmt.Fprintln(b, "  fi")
		fmt.Fprintln(b, "done")
		fmt.Fprintln(b, "")
	}

	script, err := dalec.GomodEditScript(spec)
	if err != nil {
		// This should never happen due to early validation in LoadSpec,
		// but if it does, we want to fail the build with a clear error.
		panic(errors.Wrap(err, "error generating gomod edit script"))
	}
	if script != "" {
		fmt.Fprintln(b)
		fmt.Fprint(b, script)
	}

	envKeys := dalec.SortMapKeys(t.Env)
	for _, k := range envKeys {
		v := t.Env[k]
		fmt.Fprintf(b, "export %s=\"%s\"\n", k, v)
	}

	for _, step := range t.Steps {
		writeStep(b, step)
	}

	b.WriteString("\n")
	return b.String()
}

func ToSourcesLLB(worker llb.State, spec *dalec.Spec, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) ([]llb.State, error) {
	sources, err := dalec.Sources(spec, sOpt)
	if err != nil {
		return nil, err
	}
	out := make([]llb.State, 0, len(sources))

	if err := spec.EnsureGomodPatches(sOpt, worker, opts...); err != nil {
		return nil, errors.Wrap(err, "error preparing gomod patches")
	}

	withPG := func(s string) []llb.ConstraintsOpt {
		return append(opts, dalec.ProgressGroup(s))
	}

	gomodSt, err := spec.GomodDeps(sOpt, worker, withPG("Add gomod sources")...)
	if err != nil {
		return nil, errors.Wrap(err, "error adding gomod sources")
	}

	cargohomeSt, err := spec.CargohomeDeps(sOpt, worker, withPG("Add cargohome sources")...)
	if err != nil {
		return nil, errors.Wrap(err, "error adding cargohome sources")
	}

	pipDepsSt, err := spec.PipDeps(sOpt, worker, withPG("Add pip sources")...)
	if err != nil {
		return nil, errors.Wrap(err, "error adding pip sources")
	}

	if gomodSt != nil {
		out = append(out, gomodSt.With(sourceTar(worker, gomodsName, withPG("Tar gomod deps")...)))
	}

	if cargohomeSt != nil {
		out = append(out, cargohomeSt.With(sourceTar(worker, cargohomeName, withPG("Tar cargohome deps")...)))
	}

	if pipDepsSt != nil {
		out = append(out, pipDepsSt.With(sourceTar(worker, pipDepsName, withPG("Tar pip deps")...)))
	}

	patchOpts := append([]llb.ConstraintsOpt{}, opts...)
	patchOpts = append(patchOpts, dalec.ProgressGroup("Tar gomod patches"))
	gomodPatchArchive, err := spec.GomodPatchArchive(worker, patchOpts...)
	if err != nil {
		return nil, errors.Wrap(err, "error packaging gomod patches")
	}
	if gomodPatchArchive != nil {
		out = append(out, *gomodPatchArchive)
	}

	srcsWithNodeMods, err := spec.NodeModDeps(sOpt, worker, opts...)
	if err != nil {
		return nil, errors.Wrap(err, "error preparing node deps")
	}

	sorted := dalec.SortMapKeys(sources)
	for _, k := range sorted {
		st := sources[k]
		if _, ok := srcsWithNodeMods[k]; ok {
			st = srcsWithNodeMods[k]
		}
		if dalec.SourceIsDir(spec.Sources[k]) {
			st = st.With(sourceTar(worker, k, withPG("Tar source: "+k)...))
		}
		out = append(out, st)
	}

	scriptSt := buildScriptSourceState(spec)
	if scriptSt != nil {
		out = append(out, *scriptSt)
	}

	return out, nil
}

func sourceTar(worker llb.State, key string, opts ...llb.ConstraintsOpt) llb.StateOption {
	return dalec.AsTar(worker, key+".tar.gz", opts...)
}
