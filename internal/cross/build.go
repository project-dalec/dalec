package cross

import (
	"bytes"
	"context"
	"fmt"
	"path"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
	"github.com/project-dalec/dalec/targets"
	"github.com/project-dalec/dalec/targets/linux/deb/distro"
)

const (
	BuildScriptName = "_build.sh"
	gomodsName      = "__gomods"
	cargohomeName   = "__cargohome"
	pipDepsName     = "__pipdeps"
)

// Build runs the spec on a Linux worker and stages each output in a scratch mount.
// The invocation script must run BuildScriptName before collecting its artifacts.
func Build(ctx context.Context, cfg *distro.Config, targetOS string, spec *dalec.Spec, worker llb.State, client gwclient.Client, sOpt dalec.SourceOpts, targetKey, invocation string, outputs []string, opts ...llb.ConstraintsOpt) (llb.ExecState, error) {
	opts = append(opts, frontend.IgnoreCache(client, targets.IgnoreCacheKeyPkg))
	if deps := spec.GetPackageDeps(targetKey).GetBuild(); len(deps) > 0 {
		depOpts := append(opts, deps.GetSourceLocation(worker))
		worker = worker.With(cfg.InstallBuildDeps(ctx, sOpt, spec, targetKey, depOpts...))
	}

	if err := spec.Preprocess(sOpt, worker, opts...); err != nil {
		return llb.ExecState{}, err
	}

	opts = append(opts, spec.Build.Steps.GetSourceLocation(worker))
	srcs := sources(worker, spec, sOpt, opts...)
	addGoCache(spec, targetKey)
	patched := dalec.PatchSources(worker, spec, srcs, opts...)

	runOpts := []llb.RunOption{
		dalec.ShArgs(invocation),
		llb.Dir("/build"),
		withSourcesMounted("/build", patched, spec.Sources, opts...),
		llb.AddMount("/tmp/scripts", createBuildScript(spec, opts...)),
		dalec.WithConstraints(opts...),
		llb.AddEnv("GOOS", targetOS),
		dalec.RunOptFunc(func(ei *llb.ExecInfo) {
			for _, c := range spec.Build.Caches {
				c.ToRunOption(worker, cfg.BuildCacheIdentity(), dalec.WithCacheDirConstraints(opts...)).SetRunOption(ei)
			}
		}),
		dalec.RunOptFunc(func(ei *llb.ExecInfo) {
			for k, v := range dalec.SortedMapIter(spec.Build.Env) {
				ei.State = ei.State.With(llb.AddEnv(k, v))
			}
		}),
	}
	for _, output := range outputs {
		runOpts = append(runOpts, llb.AddMount(output, llb.Scratch()))
	}
	return worker.With(dalec.SetBuildNetworkMode(spec)).Run(runOpts...), nil
}

func sources(worker llb.State, spec *dalec.Spec, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) map[string]llb.State {
	out := dalec.Sources(spec, sOpt, opts...)
	opts = append(opts, dalec.ProgressGroup("Add gomod sources"))
	gomodSt := spec.GomodDeps(sOpt, worker, opts...)
	cargohomeSt := spec.CargohomeDeps(sOpt, worker, opts...)
	for key, state := range dalec.SortedMapIter(spec.NodeModDeps(sOpt, worker, opts...)) {
		out[key] = state
	}
	pipDepsSt := spec.PipDeps(sOpt, worker, opts...)
	if gomodSt != nil {
		out[gomodsName] = *gomodSt
	}
	if cargohomeSt != nil {
		out[cargohomeName] = *cargohomeSt
	}
	if pipDepsSt != nil {
		out[pipDepsName] = *pipDepsSt
	}
	return out
}

func withSourcesMounted(dst string, states map[string]llb.State, sources map[string]dalec.Source, opts ...llb.ConstraintsOpt) llb.RunOption {
	runOpts := make([]llb.RunOption, 0, len(states))
	var files []llb.State
	for k, state := range dalec.SortedMapIter(states) {
		sourcePath := k
		src, ok := sources[k]
		if ok && !src.IsDir() {
			files = append(files, state)
			continue
		}
		if !ok {
			sourcePath = "/"
		}
		runOpts = append(runOpts, llb.AddMount(path.Join(dst, k), state, llb.SourcePath(sourcePath)))
	}
	filesSt := dalec.MergeAtPath(llb.Scratch(), files, "/", opts...)
	runOpts = append(runOpts, llb.AddMount(dst, filesSt))
	return dalec.WithRunOptions(runOpts...)
}

func addGoCache(spec *dalec.Spec, targetKey string) {
	if !spec.HasGomods() && !dalec.HasGolang(spec, targetKey) {
		return
	}
	for _, c := range spec.Build.Caches {
		if c.GoBuild != nil {
			return
		}
	}
	spec.Build.Caches = append(spec.Build.Caches, dalec.CacheConfig{GoBuild: &dalec.GoBuildCache{}})
}

func createBuildScript(spec *dalec.Spec, opts ...llb.ConstraintsOpt) llb.State {
	buf := bytes.NewBuffer(nil)
	fmt.Fprintln(buf, "#!/usr/bin/env sh")
	fmt.Fprintln(buf, "set -x")
	if spec.HasGomods() {
		fmt.Fprintln(buf, "export GOMODCACHE=\"$(pwd)/"+gomodsName+"\"")
	}
	if spec.HasCargohomes() {
		fmt.Fprintln(buf, "export CARGO_HOME=\"$(pwd)/"+cargohomeName+"\"")
	}
	if spec.HasPips() {
		fmt.Fprintln(buf, `export PIP_CACHE_DIR="$(pwd)/`+pipDepsName+`"`)
		fmt.Fprintln(buf, `for reqfile in $(find . -name 'requirements*.txt' -o -name 'pyproject.toml' -o -name 'setup.py'); do
  if [ -f "$reqfile" ]; then
    reqdir=$(dirname "$reqfile")
    mkdir -p "$reqdir/site-packages"
    case "$reqfile" in
      *.txt) python3 -m pip install --target="$reqdir/site-packages" --find-links="${PIP_CACHE_DIR}" --no-index --requirement="$reqfile" 2>/dev/null || python3 -m pip install --target="$reqdir/site-packages" --find-links="${PIP_CACHE_DIR}" --no-index --requirement="$reqfile" ;;
      *) (cd "$reqdir" && python3 -m pip install --target=site-packages --find-links="${PIP_CACHE_DIR}" --no-index . 2>/dev/null || python3 -m pip install --target=site-packages --find-links="${PIP_CACHE_DIR}" --no-index .) ;;
    esac
    export PYTHONPATH="$reqdir/site-packages:${PYTHONPATH}"
  fi
done`)
	}
	for i, step := range spec.Build.Steps {
		fmt.Fprintln(buf, "(")
		for k, v := range dalec.SortedMapIter(step.Env) {
			fmt.Fprintf(buf, "export %s=\"%s\"", k, v)
		}
		fmt.Fprintln(buf, step.Command)
		fmt.Fprint(buf, ")")
		if i < len(spec.Build.Steps)-1 {
			fmt.Fprintln(buf, " && \\")
		} else {
			fmt.Fprintln(buf)
		}
	}
	return llb.Scratch().File(llb.Mkfile(BuildScriptName, 0o770, buf.Bytes()), opts...)
}
