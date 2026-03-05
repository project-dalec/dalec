package windows

import (
	"context"
	"encoding/json"

	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	bktargets "github.com/moby/buildkit/frontend/subrequests/targets"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
	"github.com/project-dalec/dalec/targets/linux/deb/distro"
)

const (
	DefaultTargetKey              = "windowscross"
	outputKey                     = "windows"
	workerImgRef                  = "docker.io/library/ubuntu:jammy"
	WindowscrossWorkerContextName = "dalec-windowscross-worker"
)

var distroConfig = &distro.Config{
	ImageRef:       workerImgRef,
	AptCachePrefix: aptCachePrefix,
	VersionID:      "ubuntu22.04",
	ContextRef:     WindowscrossWorkerContextName,
	BuilderPackages: []string{
		"aptitude",
		"build-essential",
		"binutils-mingw-w64",
		"g++-mingw-w64-x86-64",
		"gcc",
		"git",
		"make",
		"pkg-config",
		"zip",
		"aptitude",
		"dpkg-dev",
		"debhelper",
	},
}

// Routes returns the flat routes for the Windows target, prefixed with the given prefix.
func Routes(prefix string, spec *dalec.Spec) ([]frontend.Route, error) {
	_, specDefined := spec.Targets[prefix]
	specDefined = specDefined && len(spec.Targets) > 0

	defaultPlatform := platforms.DefaultSpec()
	defaultPlatform.OS = "windows"

	return []frontend.Route{
		{
			FullPath: prefix,
			Handler:  frontend.WithDefaultPlatform(defaultPlatform, handleContainer),
			Info: frontend.Target{
				Target: bktargets.Target{
					Name:        prefix,
					Description: "Builds binaries and installs them into a Windows base image",
				},
				Hidden: true,
			},
		},
		{
			FullPath: prefix + "/zip",
			Handler:  frontend.WithDefaultPlatform(defaultPlatform, handleZip),
			Info: frontend.Target{
				Target: bktargets.Target{
					Name:        prefix + "/zip",
					Description: "Builds binaries combined into a zip file",
				},
				SpecDefined: specDefined,
			},
		},
		{
			FullPath: prefix + "/container",
			Handler:  frontend.WithDefaultPlatform(defaultPlatform, handleContainer),
			Info: frontend.Target{
				Target: bktargets.Target{
					Name:        prefix + "/container",
					Description: "Builds binaries and installs them into a Windows base image",
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
					Description: "Builds the base worker image responsible for building the package",
				},
				SpecDefined: specDefined,
			},
		},
	}, nil
}

func handleWorker(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		sOpt, err := frontend.SourceOptFromClient(ctx, client, nil)
		if err != nil {
			return nil, nil, err
		}

		pg := dalec.ProgressGroup("Handle windows worker")

		st := distroConfig.Worker(sOpt, pg)
		def, err := st.Marshal(ctx)
		if err != nil {
			return nil, nil, err
		}

		res, err := client.Solve(ctx, gwclient.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, nil, err
		}

		_, _, dt, err := client.ResolveImageConfig(ctx, workerImgRef, sourceresolver.Opt{})
		if err != nil {
			return nil, nil, err
		}

		var img dalec.DockerImageSpec
		if err := json.Unmarshal(dt, &img); err != nil {
			return nil, nil, err
		}

		ref, err := res.SingleRef()
		if err != nil {
			return nil, nil, err
		}

		return ref, &img, nil
	})
}
