package windows

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
	"github.com/project-dalec/dalec/targets"
	"github.com/project-dalec/dalec/targets/linux/deb/ubuntu"
)

const (
	outputDir       = "/tmp/output"
	buildScriptName = "_build.sh"
	aptCachePrefix  = "jammy-windowscross"
	distroVersionID = ubuntu.JammyVersionID
)

func handleZip(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		sOpt, err := frontend.SourceOptFromClient(ctx, client, nil)
		if err != nil {
			return nil, nil, err
		}

		if err := validateZipArtifacts(spec, targetKey); err != nil {
			return nil, nil, err
		}

		pg := dalec.ProgressGroup("Build windows container: " + spec.Name)
		worker := distroConfig.Worker(sOpt, pg)

		bin := buildBinaries(ctx, spec, worker, client, sOpt, targetKey, pg)

		st := getZipLLB(worker, platform, spec, targetKey, bin, pg)

		def, err := st.Marshal(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("error marshalling llb: %w", err)
		}

		res, err := client.Solve(ctx, gwclient.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, nil, err
		}
		ref, err := res.SingleRef()
		return ref, &dalec.DockerImageSpec{}, err
	})
}

const (
	gomodsName    = "__gomods"
	cargohomeName = "__cargohome"
	pipDepsName   = "__pipdeps"
)

func sources(worker llb.State, spec *dalec.Spec, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) map[string]llb.State {
	out := dalec.Sources(spec, sOpt, opts...)

	opts = append(opts, dalec.ProgressGroup("Add gomod sources"))

	gomodSt := spec.GomodDeps(sOpt, worker, opts...)

	cargohomeSt := spec.CargohomeDeps(sOpt, worker, opts...)

	srcsWithNodeMods := spec.NodeModDeps(sOpt, worker, opts...)
	sorted := dalec.SortMapKeys(srcsWithNodeMods)

	for _, key := range sorted {
		out[key] = srcsWithNodeMods[key]
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

	sorted := dalec.SortMapKeys(states)

	var files []llb.State

	for _, k := range sorted {
		state := states[k]

		dest := filepath.Join(dst, k)
		sourcePath := k

		src, ok := sources[k]
		if ok && !src.IsDir() {
			// If this is a file, we need to have some special handling.
			// Specifically if we just mount the file directly there are limitations
			// on what can be done with it (e.g. it can get "device or resource busy" errors).
			files = append(files, states[k])
			continue
		}

		if !ok {
			// In some cases we have a state that is not in the sources map (e.g. source generators)
			// In these cases,t he data is not nested under `k` like sources are, so adjust the path accordingly
			sourcePath = "/"
		}

		runOpts = append(runOpts, llb.AddMount(dest, state, llb.SourcePath(sourcePath)))
	}

	// Merge all the files into a single state that gets mounted in as a directory.
	filesSt := dalec.MergeAtPath(llb.Scratch(), files, "/", opts...)
	runOpts = append(runOpts, llb.AddMount(dst, filesSt))

	return dalec.WithRunOptions(runOpts...)
}

func addGoCache(spec *dalec.Spec, targetKey string) {
	if !spec.HasGomods() && !dalec.HasGolang(spec, targetKey) {
		return
	}

	addCache := true
	for _, c := range spec.Build.Caches {
		if c.GoBuild != nil {
			addCache = false
			break
		}
	}
	if !addCache {
		return
	}

	spec.Build.Caches = append(spec.Build.Caches, dalec.CacheConfig{
		GoBuild: &dalec.GoBuildCache{},
	})
}

func buildBinaries(ctx context.Context, spec *dalec.Spec, worker llb.State, client gwclient.Client, sOpt dalec.SourceOpts, targetKey string, opts ...llb.ConstraintsOpt) llb.State {
	opts = append(opts, frontend.IgnoreCache(client, targets.IgnoreCacheKeyPkg))

	deps := spec.GetPackageDeps(targetKey).GetBuild()
	if len(deps) > 0 {
		opts := append(opts, deps.GetSourceLocation(worker))
		worker = worker.With(distroConfig.InstallBuildDeps(ctx, sOpt, spec, targetKey, opts...))
	}

	// Preprocess the spec to generate patches for gomod edits and other generators
	// This must happen after build deps are installed so Go is available
	if err := spec.Preprocess(sOpt, worker, opts...); err != nil {
		return dalec.ErrorState(worker, err)
	}

	packageOpts := append([]llb.ConstraintsOpt(nil), opts...)

	// Apply source map constraints for build steps
	opts = append(opts, spec.Build.Steps.GetSourceLocation(worker))

	sources := sources(worker, spec, sOpt, opts...)

	addGoCache(spec, targetKey)
	// No automatic cargo cache setup - cargo cache is opt-in only due to security considerations

	patched := dalec.PatchSources(worker, spec, sources, opts...)
	buildScript := createBuildScript(spec, opts...)
	script := generateInvocationScript(spec, targetKey)
	pkgs := windowsPackages(spec, targetKey)

	builder := worker.With(dalec.SetBuildNetworkMode(spec))
	runOpts := []llb.RunOption{
		dalec.ShArgs(script.String()),
		llb.Dir("/build"),
		withSourcesMounted("/build", patched, spec.Sources, opts...),
		llb.AddMount("/tmp/scripts", buildScript),
		dalec.WithConstraints(opts...),
		// We could check if we even need the var (ie there are gomods) but this
		// is a fine default since we are expecting windows binaries. This
		// means if someone eneds to build non-windows tooling as part of the
		// build then they will need to set GOOS=linux manually.
		// As such, this must come before the env vars from the spec are set.
		llb.AddEnv("GOOS", "windows"),
		dalec.RunOptFunc(func(ei *llb.ExecInfo) {
			for _, c := range spec.Build.Caches {
				c.ToRunOption(worker, path.Join(distroVersionID, targetKey), dalec.WithCacheDirConstraints(opts...)).SetRunOption(ei)
			}
		}),
		dalec.RunOptFunc(func(ei *llb.ExecInfo) {
			for k, v := range dalec.SortedMapIter(spec.Build.Env) {
				ei.State = ei.State.With(llb.AddEnv(k, v))
			}
		}),
	}
	for _, pkg := range pkgs {
		runOpts = append(runOpts, llb.AddMount(pkg.buildOutputDir(), llb.Scratch()))
	}

	built := builder.Run(runOpts...)
	packageStates := make([]llb.State, 0, len(pkgs))
	for _, pkg := range pkgs {
		original := built.GetMount(pkg.buildOutputDir())
		signed := frontend.MaybeSign(ctx, client, original, spec, targetKey, sOpt, packageOpts...)

		// Signers may return only changed files. Preserve the original package
		// and overlay the signer output so replacements win.
		packageState := original.File(llb.Copy(signed, "/", "/"), packageOpts...)
		packageStates = append(packageStates, llb.Scratch().File(
			llb.Copy(packageState, "/", pkg.internalDir(), dalec.WithCreateDestPath()),
			packageOpts...,
		))
	}

	return dalec.MergeAtPath(llb.Scratch(), packageStates, "/", packageOpts...)
}

func getZipLLB(worker llb.State, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string, artifacts llb.State, opts ...llb.ConstraintsOpt) llb.State {
	const artifactsDir = "/tmp/artifacts"

	// Each package is zipped into a separate
	// "<name>_<version>-<revision>_<arch>.zip" file.
	script := &strings.Builder{}
	fmt.Fprintln(script, "set -ex")
	for _, pkg := range windowsPackages(spec, targetKey) {
		fileName := fmt.Sprintf("%s_%s-%s_%s.zip", pkg.Name, spec.Version, spec.Revision, platform.Architecture)
		outName := filepath.Join(outputDir, fileName)
		srcDir := path.Join(artifactsDir, pkg.internalDir())
		fmt.Fprintf(script, "(cd %q && find . -maxdepth 1 -type f -exec zip %q {} +)\n", srcDir, outName)
	}

	zipped := worker.Run(
		dalec.ShArgs(script.String()),
		llb.AddMount(artifactsDir, artifacts),
		dalec.WithConstraints(opts...),
	).AddMount(outputDir, llb.Scratch())
	return zipped
}

// windowsPackage pairs a resolved package name with the binary artifacts that
// go into it for a windows target.
type windowsPackage struct {
	// Index is the package's deterministic position: primary first, then
	// supplemental packages sorted by map key.
	Index int
	// Name is the resolved package name (primary package name or the
	// supplemental package's resolved name).
	Name string
	// Binaries are the binary artifacts that belong to this package.
	Binaries map[string]dalec.ArtifactConfig
	// Primary is true for the spec's primary package.
	Primary bool
}

func (p windowsPackage) buildOutputDir() string {
	return path.Join(outputDir, fmt.Sprintf("%d", p.Index))
}

func (p windowsPackage) internalDir() string {
	return path.Join("/", fmt.Sprintf("%d", p.Index))
}

// windowsPackages returns the ordered set of packages produced for a windows
// target: the primary package first, followed by supplemental packages sorted
// by their map key. Windows packages only ship binaries.
func windowsPackages(spec *dalec.Spec, targetKey string) []windowsPackage {
	pkgs := []windowsPackage{{
		Index:    0,
		Name:     spec.Name,
		Binaries: spec.GetArtifacts(targetKey).Binaries,
		Primary:  true,
	}}

	for key, p := range dalec.GetSubPackagesForTarget(spec, targetKey) {
		var binaries map[string]dalec.ArtifactConfig
		if p.Artifacts != nil {
			binaries = p.Artifacts.Binaries
		}
		pkgs = append(pkgs, windowsPackage{
			Index:    len(pkgs),
			Name:     p.ResolvedName(spec.Name, key),
			Binaries: binaries,
		})
	}

	return pkgs
}

func generateInvocationScript(spec *dalec.Spec, targetKey string) *strings.Builder {
	script := &strings.Builder{}
	fmt.Fprintln(script, "#!/usr/bin/env sh")
	fmt.Fprintln(script, "set -ex")
	fmt.Fprintf(script, "/tmp/scripts/%s\n", buildScriptName)

	// Each output path is a separate scratch mount, so every package state has
	// the flat root expected by signers. Files are copied (not moved) so a build
	// output shared by multiple packages stays available.
	for _, pkg := range windowsPackages(spec, targetKey) {
		writePackageArtifacts(script, pkg.buildOutputDir(), pkg)
	}

	return script
}

// writePackageArtifacts writes the commands that stage a single package's
// binaries under destDir.
func writePackageArtifacts(script *strings.Builder, destDir string, pkg windowsPackage) {
	fmt.Fprintf(script, "mkdir -p '%s'\n", destDir)

	for bin, config := range dalec.SortedMapIter(pkg.Binaries) {
		dest := path.Join(destDir, config.ResolveName(bin))
		fmt.Fprintf(script, "cp -r '%s' '%s'\n", bin, dest)
		if config.Permissions.Perm() != 0 {
			fmt.Fprintf(script, "chmod %o '%s'\n", config.Permissions.Perm(), dest)
		}
	}
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
		// Set up pip environment and install dependencies during build
		fmt.Fprintln(buf, "# Set up pip environment")
		fmt.Fprintln(buf, "export PIP_CACHE_DIR=\"$(pwd)/"+pipDepsName+"\"")
		fmt.Fprintln(buf, "")
		fmt.Fprintln(buf, "# Install pip dependencies from cache")
		fmt.Fprintln(buf, "for reqfile in $(find . -name 'requirements*.txt' -o -name 'pyproject.toml' -o -name 'setup.py'); do")
		fmt.Fprintln(buf, "  if [ -f \"$reqfile\" ]; then")
		fmt.Fprintln(buf, "    reqdir=$(dirname \"$reqfile\")")
		fmt.Fprintln(buf, "    mkdir -p \"$reqdir/site-packages\"")
		fmt.Fprintln(buf, "    case \"$reqfile\" in")
		fmt.Fprintln(buf, "      *.txt) python3 -m pip install --target=\"$reqdir/site-packages\" --find-links=\"${PIP_CACHE_DIR}\" --no-index --requirement=\"$reqfile\" 2>/dev/null || python3 -m pip install --target=\"$reqdir/site-packages\" --find-links=\"${PIP_CACHE_DIR}\" --no-index --requirement=\"$reqfile\" ;;")
		fmt.Fprintln(buf, "      *) (cd \"$reqdir\" && python3 -m pip install --target=site-packages --find-links=\"${PIP_CACHE_DIR}\" --no-index . 2>/dev/null || python3 -m pip install --target=site-packages --find-links=\"${PIP_CACHE_DIR}\" --no-index .) ;;")
		fmt.Fprintln(buf, "    esac")
		fmt.Fprintln(buf, "    export PYTHONPATH=\"$reqdir/site-packages:${PYTHONPATH}\"")
		fmt.Fprintln(buf, "  fi")
		fmt.Fprintln(buf, "done")
		fmt.Fprintln(buf, "")
	}

	for i, step := range spec.Build.Steps {
		fmt.Fprintln(buf, "(")

		for k, v := range dalec.SortedMapIter(step.Env) {
			fmt.Fprintf(buf, "export %s=\"%s\"", k, v)
		}

		fmt.Fprintln(buf, step.Command)
		fmt.Fprintf(buf, ")")

		if i < len(spec.Build.Steps)-1 {
			fmt.Fprintln(buf, " && \\")
			continue
		}

		fmt.Fprintf(buf, "\n")
	}

	return llb.Scratch().
		File(llb.Mkfile(buildScriptName, 0o770, buf.Bytes()), opts...)
}
