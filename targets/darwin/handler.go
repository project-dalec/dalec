package darwin

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	bktargets "github.com/moby/buildkit/frontend/subrequests/targets"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
	"github.com/project-dalec/dalec/internal/cross"
	"github.com/project-dalec/dalec/targets/linux/deb/distro"
	"github.com/project-dalec/dalec/targets/linux/deb/ubuntu"
)

const (
	DefaultTargetKey  = "darwincross"
	WorkerContextName = "dalec-darwincross-worker"
	outputDir         = "/tmp/output"
)

var distroConfig = &distro.Config{
	ImageRef:       "docker.io/library/ubuntu:noble",
	AptCachePrefix: "noble-darwincross",
	VersionID:      ubuntu.NobleConfig.VersionID,
	CacheIdentity:  ubuntu.NobleConfig.VersionID + "-darwincross",
	ContextRef:     WorkerContextName,
	BuilderPackages: []string{
		"aptitude", "build-essential", "debhelper", "git", "make", "pkg-config", "zip",
	},
}

// Routes exposes Darwin binary archives built on a Linux worker. It does not
// provide an Apple SDK or cross C compiler; specs must supply one if needed.
func Routes(prefix string, spec *dalec.Spec) ([]frontend.Route, error) {
	_, specDefined := spec.Targets[prefix]
	platform := platforms.DefaultSpec()
	platform.OS = "darwin"
	return []frontend.Route{
		{
			FullPath: prefix + "/zip",
			Handler:  frontend.WithDefaultPlatform(platform, handleZip),
			Info: frontend.Target{
				Target: bktargets.Target{
					Name:        prefix + "/zip",
					Description: "Cross-compiles Darwin binaries and packages them in zip archives",
					Default:     true,
				},
				SpecDefined: specDefined,
			},
		},
		{
			FullPath: prefix + "/worker",
			Handler:  handleWorker,
			Info: frontend.Target{
				Target: bktargets.Target{
					Name:        prefix + "/worker",
					Description: "Builds the Linux worker used to cross-compile Darwin binaries",
				},
				SpecDefined: specDefined,
			},
		},
	}, nil
}

func worker(specOpts dalec.SourceOpts, opts ...llb.ConstraintsOpt) llb.State {
	// TargetPlatform describes the archive, not the executor. Never resolve a
	// Linux worker image for darwin/arm64 or darwin/amd64.
	specOpts.TargetPlatform = nil
	return distroConfig.Worker(specOpts, opts...)
}

func handleWorker(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, _ *ocispecs.Platform, _ *dalec.Spec, _ string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		sOpt, err := frontend.SourceOptFromClient(ctx, client, nil)
		if err != nil {
			return nil, nil, err
		}
		def, err := worker(sOpt).Marshal(ctx)
		if err != nil {
			return nil, nil, err
		}
		res, err := client.Solve(ctx, gwclient.SolveRequest{Definition: def.ToPB()})
		if err != nil {
			return nil, nil, err
		}
		platform := platforms.DefaultSpec()
		_, _, data, err := client.ResolveImageConfig(ctx, distroConfig.ImageRef, sourceresolver.Opt{
			ImageOpt: &sourceresolver.ResolveImageOpt{Platform: &platform},
		})
		if err != nil {
			return nil, nil, err
		}
		var config dalec.DockerImageSpec
		if err := json.Unmarshal(data, &config); err != nil {
			return nil, nil, err
		}
		ref, err := res.SingleRef()
		return ref, &config, err
	})
}

func validate(spec *dalec.Spec, targetKey string, platform *ocispecs.Platform) error {
	if platform == nil || platform.OS != "darwin" || (platform.Architecture != "amd64" && platform.Architecture != "arm64") {
		return fmt.Errorf("%s/zip requires darwin/amd64 or darwin/arm64, got %v", targetKey, platform)
	}
	for _, pkg := range packages(spec, targetKey) {
		if len(pkg.artifacts.Binaries) == 0 {
			return fmt.Errorf("package %q produces no binaries; %s/zip requires at least one binary per package", pkg.name, targetKey)
		}
	}
	return nil
}

func handleZip(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		if err := validate(spec, targetKey, platform); err != nil {
			return nil, nil, err
		}
		sOpt, err := frontend.SourceOptFromClient(ctx, client, nil)
		if err != nil {
			return nil, nil, err
		}
		pg := dalec.ProgressGroup("Build Darwin archives: " + spec.Name)
		work := worker(sOpt, pg)
		pkgs := packages(spec, targetKey)
		invocation, outputs := invocationScript(pkgs)
		built, err := cross.Build(ctx, distroConfig, "darwin", spec, work, client, sOpt, targetKey, invocation, outputs, pg)
		if err != nil {
			return nil, nil, err
		}
		archives := make([]llb.State, 0, len(pkgs))
		for _, pkg := range pkgs {
			original := built.GetMount(pkg.output)
			signed := frontend.MaybeSign(ctx, client, original, spec, targetKey, sOpt, pg)
			artifacts := original.File(llb.Copy(signed, "/", "/"), pg)
			filename := fmt.Sprintf("%s_%s-%s_darwin_%s.zip", pkg.name, spec.Version, spec.Revision, platform.Architecture)
			archive := work.Run(
				dalec.ShArgs("set -eu; zip -r "+shellQuote(path.Join(outputDir, filename))+" ."),
				llb.Dir("/tmp/artifacts"),
				llb.AddMount("/tmp/artifacts", artifacts, llb.Readonly),
				pg,
			).AddMount(outputDir, llb.Scratch())
			archives = append(archives, archive)
		}
		def, err := dalec.MergeAtPath(llb.Scratch(), archives, "/", pg).Marshal(ctx)
		if err != nil {
			return nil, nil, err
		}
		res, err := client.Solve(ctx, gwclient.SolveRequest{Definition: def.ToPB()})
		if err != nil {
			return nil, nil, err
		}
		ref, err := res.SingleRef()
		return ref, &dalec.DockerImageSpec{}, err
	})
}

type archivePackage struct {
	name      string
	output    string
	artifacts dalec.Artifacts
}

func packages(spec *dalec.Spec, targetKey string) []archivePackage {
	pkgs := []archivePackage{{name: spec.Name, output: path.Join(outputDir, "0"), artifacts: spec.GetArtifacts(targetKey)}}
	for key, pkg := range dalec.GetSubPackagesForTarget(spec, targetKey) {
		artifacts := dalec.Artifacts{}
		if pkg.Artifacts != nil {
			artifacts = *pkg.Artifacts
		}
		pkgs = append(pkgs, archivePackage{
			name:      pkg.ResolvedName(spec.Name, key),
			output:    path.Join(outputDir, fmt.Sprint(len(pkgs))),
			artifacts: artifacts,
		})
	}
	return pkgs
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func invocationScript(pkgs []archivePackage) (string, []string) {
	var script strings.Builder
	fmt.Fprintln(&script, "set -eu")
	fmt.Fprintf(&script, "/tmp/scripts/%s\n", cross.BuildScriptName)
	outputs := make([]string, 0, len(pkgs))
	for _, pkg := range pkgs {
		outputs = append(outputs, pkg.output)
		for _, category := range []struct {
			dir   string
			files map[string]dalec.ArtifactConfig
		}{
			{"", pkg.artifacts.Binaries},
			{"licenses", pkg.artifacts.Licenses},
			{"docs", pkg.artifacts.Docs},
		} {
			for src, config := range dalec.SortedMapIter(category.files) {
				dst := path.Join(pkg.output, category.dir, config.SubPath, config.ResolveName(src))
				fmt.Fprintf(&script, "mkdir -p %s\ncp -r %s %s\n", shellQuote(path.Dir(dst)), shellQuote(src), shellQuote(dst))
				if config.Permissions.Perm() != 0 {
					fmt.Fprintf(&script, "chmod %o %s\n", config.Permissions.Perm(), shellQuote(dst))
				}
			}
		}
	}
	return script.String(), outputs
}
