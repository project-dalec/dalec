package distro

import (
	"context"
	"fmt"
	"slices"

	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests/targets"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
	"github.com/project-dalec/dalec/packaging/linux/rpm"
)

// DebugWorker returns a worker image with the build dependencies specified in `spec` installed,
// if needed.
// It is most useful for `HandleSources` handler in which we aren't building a full worker image with
// build dependencies because we aren't executing build steps, but we may still have source generators
// which depend on `build` dependencies in the spec in order to run.
func (c *Config) DebugWorker(ctx context.Context, client gwclient.Client, spec *dalec.Spec, targetKey string, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) llb.State {
	worker := c.Worker(sOpt, opts...)

	deps := spec.GetPackageDeps(targetKey).GetBuild()
	pkgNames := dalec.SortMapKeys(deps)
	if spec.HasGomods() {
		if !dalec.HasGolang(spec, targetKey) {
			return dalec.ErrorState(worker, errors.New("spec contains go modules but does not have golang in build deps"))
		}
	}

	if spec.HasCargohomes() {
		hasRust := func(s string) bool {
			return s == "rust"
		}
		if !slices.ContainsFunc(pkgNames, hasRust) {
			return dalec.ErrorState(worker, errors.New("spec contains cargo homes but does not have rust in build deps"))
		}
	}

	if spec.HasNodeMods() {
		if !dalec.HasNpm(spec, targetKey) {
			return dalec.ErrorState(worker, errors.New("spec contains node modules but does not have npm in build deps"))
		}
	}

	worker = worker.With(c.InstallBuildDeps(spec, sOpt, targetKey))
	return worker
}

func (c *Config) HandleBuildroot(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		if err := rpm.ValidateSpec(spec); err != nil {
			return nil, nil, fmt.Errorf("rpm: invalid spec: %w", err)
		}

		pg := dalec.ProgressGroup("Setting up " + targetKey + " rpm buildroot: " + spec.Name)
		sOpt, err := frontend.SourceOptFromClient(ctx, client, platform)
		if err != nil {
			return nil, nil, err
		}

		pc := dalec.Platform(platform)
		worker := c.Worker(sOpt, pg, pc)

		worker = worker.With(c.InstallBuildDeps(spec, sOpt, targetKey, pg, pc))

		br := rpm.BuildRoot(worker, spec, sOpt, targetKey, pg)

		def, err := br.Marshal(ctx, pc)
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
		if err != nil {
			return nil, nil, err
		}

		if platform == nil {
			p := platforms.DefaultSpec()
			platform = &p
		}

		return ref, &dalec.DockerImageSpec{Image: ocispecs.Image{Platform: *platform}}, nil
	})
}

func (c *Config) HandleSources(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		sOpt, err := frontend.SourceOptFromClient(ctx, client, platform)
		if err != nil {
			return nil, nil, err
		}

		pc := dalec.Platform(platform)
		worker := c.DebugWorker(ctx, client, spec, targetKey, sOpt, pc)

		sources := rpm.Sources(worker, spec, sOpt, pc)

		// Now we can merge sources into the desired path
		st := dalec.MergeAtPath(llb.Scratch(), sources, "/SOURCES")

		def, err := st.Marshal(ctx, pc)
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
		if err != nil {
			return nil, nil, err
		}
		return ref, &dalec.DockerImageSpec{}, nil
	})
}

func (c *Config) HandleSpec(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		pc := dalec.Platform(platform)
		st := rpm.RPMSpec(spec, llb.Scratch(), targetKey, "", pc)

		def, err := st.Marshal(ctx, pc)
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

// HandleDebug returns a build function that adds support for some debugging targets for RPM builds.
func (c *Config) HandleDebug() gwclient.BuildFunc {
	return func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
		var r frontend.BuildMux

		r.Add("buildroot", c.HandleBuildroot, &targets.Target{
			Name:        "buildroot",
			Description: "Outputs an rpm buildroot suitable for passing to rpmbuild.",
		})

		r.Add("sources", c.HandleSources, &targets.Target{
			Name:        "sources",
			Description: "Outputs all the sources specified in the spec file in the format given to rpmbuild.",
		})

		r.Add("spec", c.HandleSpec, &targets.Target{
			Name:        "spec",
			Description: "Outputs the generated RPM spec file",
		})

		return r.Handle(ctx, client)
	}
}
