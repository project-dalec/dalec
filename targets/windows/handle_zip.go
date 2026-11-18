package windows

import (
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
	"github.com/project-dalec/dalec/internal/cross"
)

const (
	outputDir       = "/tmp/output"
	buildScriptName = cross.BuildScriptName
	aptCachePrefix  = "jammy-windowscross"
)

func (h routeHandlers) handleZip(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		sOpt, err := frontend.SourceOptFromClient(ctx, client, nil)
		if err != nil {
			return nil, nil, err
		}
		if err := validateZipArtifacts(spec, targetKey); err != nil {
			return nil, nil, err
		}
		pg := dalec.ProgressGroup("Build windows container: " + spec.Name)
		worker := h.distro.Worker(sOpt, pg)
		bin := h.buildBinaries(ctx, spec, worker, client, sOpt, targetKey, pg)
		st := getZipLLB(worker, platform, spec, targetKey, bin, pg)
		def, err := st.Marshal(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("error marshalling llb: %w", err)
		}
		res, err := client.Solve(ctx, gwclient.SolveRequest{Definition: def.ToPB()})
		if err != nil {
			return nil, nil, err
		}
		ref, err := res.SingleRef()
		return ref, &dalec.DockerImageSpec{}, err
	})
}

func (h routeHandlers) buildBinaries(ctx context.Context, spec *dalec.Spec, worker llb.State, client gwclient.Client, sOpt dalec.SourceOpts, targetKey string, opts ...llb.ConstraintsOpt) llb.State {
	pkgs := windowsPackages(spec, targetKey)
	outputs := make([]string, 0, len(pkgs))
	for _, pkg := range pkgs {
		outputs = append(outputs, pkg.buildOutputDir())
	}
	built, err := cross.Build(ctx, h.distro, "windows", spec, worker, client, sOpt, targetKey, generateInvocationScript(spec, targetKey).String(), outputs, opts...)
	if err != nil {
		return dalec.ErrorState(worker, err)
	}
	packageStates := make([]llb.State, 0, len(pkgs))
	for _, pkg := range pkgs {
		original := built.GetMount(pkg.buildOutputDir())
		signed := frontend.MaybeSign(ctx, client, original, spec, targetKey, sOpt, opts...)

		// Signers may return only changed files. Preserve the original package
		// and overlay the signer output so replacements win.
		packageState := original.File(llb.Copy(signed, "/", "/"), opts...)
		packageStates = append(packageStates, llb.Scratch().File(
			llb.Copy(packageState, "/", pkg.internalDir(), dalec.WithCreateDestPath()),
			opts...,
		))
	}
	return dalec.MergeAtPath(llb.Scratch(), packageStates, "/", opts...)
}

func getZipLLB(worker llb.State, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string, artifacts llb.State, opts ...llb.ConstraintsOpt) llb.State {
	const artifactsDir = "/tmp/artifacts"
	script := &strings.Builder{}
	fmt.Fprintln(script, "set -ex")
	for _, pkg := range windowsPackages(spec, targetKey) {
		fileName := fmt.Sprintf("%s_%s-%s_%s.zip", pkg.Name, spec.Version, spec.Revision, platform.Architecture)
		outName := filepath.Join(outputDir, fileName)
		srcDir := path.Join(artifactsDir, pkg.internalDir())
		fmt.Fprintf(script, "(cd %q && find . -maxdepth 1 -type f -exec zip %q {} +)\n", srcDir, outName)
	}
	return worker.Run(
		dalec.ShArgs(script.String()),
		llb.AddMount(artifactsDir, artifacts),
		dalec.WithConstraints(opts...),
	).AddMount(outputDir, llb.Scratch())
}

// windowsPackage pairs a resolved package name with the binary artifacts that
// go into it for a windows target.
type windowsPackage struct {
	Index    int
	Name     string
	Binaries map[string]dalec.ArtifactConfig
	Primary  bool
}

func (p windowsPackage) buildOutputDir() string {
	return path.Join(outputDir, fmt.Sprintf("%d", p.Index))
}

func (p windowsPackage) internalDir() string {
	return path.Join("/", fmt.Sprintf("%d", p.Index))
}

// windowsPackages returns the primary package followed by supplemental packages
// sorted by map key. Windows packages only ship binaries.
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
			Index: len(pkgs), Name: p.ResolvedName(spec.Name, key), Binaries: binaries,
		})
	}
	return pkgs
}

func generateInvocationScript(spec *dalec.Spec, targetKey string) *strings.Builder {
	script := &strings.Builder{}
	fmt.Fprintln(script, "#!/usr/bin/env sh")
	fmt.Fprintln(script, "set -ex")
	fmt.Fprintf(script, "/tmp/scripts/%s\n", buildScriptName)
	for _, pkg := range windowsPackages(spec, targetKey) {
		writePackageArtifacts(script, pkg.buildOutputDir(), pkg)
	}
	return script
}

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
