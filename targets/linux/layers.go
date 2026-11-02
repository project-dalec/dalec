package linux

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
	"github.com/project-dalec/dalec/internal/layersplit"
)

// layerSplitter is implemented by distros which can split a container root
// filesystem into one layer per package set.
type layerSplitter interface {
	// LayerSplitDumpScript returns a shell script which takes the root
	// filesystem path, the spec packages path and an output path as
	// arguments, and writes the package manager data read by
	// [layersplit.BuildPlan] to the output path.
	LayerSplitDumpScript() string

	// LayerSplitBasePackages returns the names of the distro's mandatory base
	// packages installed in the container built for spec. It returns false
	// when the container is not built from scratch (for example when it is
	// built on top of a base image), in which case its layers are not split.
	LayerSplitBasePackages(spec *dalec.Spec, targetKey string) ([]string, bool)
}

const (
	layerSplitFrontendPath = "/tmp/internal/dalec/layers/frontend"
	layerSplitRootPath     = "/tmp/internal/dalec/layers/rootfs"
	layerSplitPkgsPath     = "/tmp/internal/dalec/layers/pkgs"
	layerSplitBasePath     = "/tmp/internal/dalec/layers/base"
	layerSplitPlanPath     = "/tmp/internal/dalec/layers/plan"
	layerSplitOutPath      = "/tmp/internal/dalec/layers/out"
	layerSplitActualPath   = "/tmp/internal/dalec/layers/actual"

	layerSplitBaseFile = "base.txt"
)

// splitLayersResult is the output of [splitLayers].
type splitLayersResult struct {
	// State is the merged image root filesystem made of one layer per group.
	State llb.State
	// Verify must be required by the final output so the layer split is
	// checked against the installed root filesystem.
	Verify llb.State
	// Groups describes each layer of State, in order.
	Groups []layersplit.Group
}

// maybeSplitLayers splits the container root filesystem into one layer per
// package set when the distro and builder support it. It returns nil when the
// container should be a single layer, as it is when built on top of a base
// image or when BuildKit does not support diff and merge.
func maybeSplitLayers(ctx context.Context, client gwclient.Client, sOpt dalec.SourceOpts, c DistroConfig, spec *dalec.Spec, targetKey string, ctr, pkgSt llb.State, opts ...llb.ConstraintsOpt) (*splitLayersResult, error) {
	ls, ok := c.(layerSplitter)
	if !ok {
		return nil, nil
	}
	bi, err := spec.GetSingleBase(targetKey)
	if err != nil {
		return nil, err
	}
	if bi != nil {
		return nil, nil
	}
	base, ok := ls.LayerSplitBasePackages(spec, targetKey)
	if !ok {
		return nil, nil
	}
	if !frontend.SupportsDiffMerge(client) {
		return nil, nil
	}

	worker := c.Worker(sOpt, dalec.Platform(sOpt.TargetPlatform), dalec.WithConstraints(opts...))
	return splitLayers(ctx, client, worker, ls, base, ctr, pkgSt, opts...)
}

// splitLayers splits the installed container root filesystem rootfs into one
// layer per package set plus a final state layer.
func splitLayers(ctx context.Context, client gwclient.Client, worker llb.State, ls layerSplitter, base []string, rootfs, pkgs llb.State, opts ...llb.ConstraintsOpt) (*splitLayersResult, error) {
	opts = append(opts, dalec.ProgressGroup("Split image layers"), frontend.IgnoreCache(client))
	frontendSt, err := layerSplitFrontend(ctx, client)
	if err != nil {
		return nil, err
	}

	// Solve the installed root filesystem once and use that exact result for
	// every step below. Otherwise steps that ignore the cache (for example
	// with --no-cache-filter) could run the install again between the plan
	// and the final solve, and the split would not match what is verified.
	rootfs, err = solveToState(ctx, client, rootfs, opts...)
	if err != nil {
		return nil, errors.Wrap(err, "building container root filesystem")
	}

	// The split steps always run instead of being loaded from the cache.
	// BuildKit keys execs with read-only mounts by a content checksum that
	// ignores modification times, which could reuse a split of an earlier
	// install whose generated files differ from this one. The steps are fast
	// and deterministic, so unchanged package sets still produce identical
	// layers.
	splitOpts := append(slices.Clone(opts), llb.IgnoreCache)

	withFrontend := llb.AddMount(layerSplitFrontendPath, frontendSt, llb.SourcePath("/frontend"), llb.Readonly)
	withRoot := llb.AddMount(layerSplitRootPath, rootfs, llb.Readonly)

	baseList := llb.Scratch().File(
		llb.Mkfile(layerSplitBaseFile, 0o644, []byte(strings.Join(base, "\n")+"\n")),
		dalec.WithConstraints(opts...),
	)

	script := ls.LayerSplitDumpScript() + "\n" +
		layerSplitFrontendPath + " " + layersplit.CmdName + " plan" +
		" --root " + layerSplitRootPath +
		" --dump " + layerSplitOutPath +
		" --base " + layerSplitBasePath + "/" + layerSplitBaseFile +
		" --out " + layerSplitOutPath + "\n"

	planSt := worker.Run(
		llb.Args([]string{"/bin/sh", "-c", script, "layer-split", layerSplitRootPath, layerSplitPkgsPath, layerSplitOutPath}),
		withFrontend,
		withRoot,
		llb.AddMount(layerSplitPkgsPath, pkgs, llb.Readonly),
		llb.AddMount(layerSplitBasePath, baseList, llb.Readonly),
		llb.WithCustomName("Plan image layers"),
		dalec.WithConstraints(splitOpts...),
	).AddMount(layerSplitOutPath, llb.Scratch())

	def, err := planSt.Marshal(ctx, dalec.WithConstraints(opts...))
	if err != nil {
		return nil, err
	}
	res, err := client.Solve(ctx, gwclient.SolveRequest{Definition: def.ToPB()})
	if err != nil {
		return nil, errors.Wrap(err, "planning image layers")
	}
	ref, err := res.SingleRef()
	if err != nil {
		return nil, err
	}
	dt, err := ref.ReadFile(ctx, gwclient.ReadRequest{Filename: layersplit.PlanFile})
	if err != nil {
		return nil, errors.Wrap(err, "reading layer plan")
	}
	plan, err := layersplit.UnmarshalPlan(dt)
	if err != nil {
		return nil, err
	}

	// Directory timestamps in layers are normalized so identical package
	// sets produce identical layers across builds.
	dirMtime := "0"
	if v, ok := frontend.GetBuildArg(client, "SOURCE_DATE_EPOCH"); ok && v != "" {
		if _, err := strconv.ParseInt(v, 10, 64); err != nil {
			return nil, errors.Wrap(err, "invalid SOURCE_DATE_EPOCH")
		}
		dirMtime = v
	}

	copyRun := worker.Run(
		llb.Args([]string{
			layerSplitFrontendPath, layersplit.CmdName, "copy",
			"--root", layerSplitRootPath,
			"--plan", layerSplitPlanPath,
			"--out", layerSplitOutPath,
			"--dir-mtime", dirMtime,
		}),
		withFrontend,
		withRoot,
		llb.AddMount(layerSplitPlanPath, planSt, llb.Readonly),
		llb.WithCustomName("Copy files into image layers"),
		dalec.WithConstraints(splitOpts...),
	)

	// Each output mount becomes one layer. Unchanged package sets produce
	// byte-identical layers because the copy preserves file metadata from the
	// packages and normalizes directory timestamps.
	//
	// Note: the layers must not be passed through a file op copy to get
	// content-based cache keys. BuildKit's content checksum ignores
	// modification times, so a cache hit could reuse a layer whose file
	// timestamps differ from the installed root filesystem.
	layers := make([]llb.State, 0, len(plan.Groups))
	for i := range plan.Groups {
		layers = append(layers, copyRun.AddMount(layerSplitOutPath+"/"+strconv.Itoa(i), llb.Scratch()))
	}

	merged := layers[0]
	if len(layers) > 1 {
		merged = llb.Merge(layers, dalec.WithConstraints(opts...))
	}

	verify := worker.Run(
		llb.Args([]string{
			layerSplitFrontendPath, layersplit.CmdName, "verify",
			"--expected", layerSplitRootPath,
			"--actual", layerSplitActualPath,
		}),
		withFrontend,
		withRoot,
		llb.AddMount(layerSplitActualPath, merged, llb.Readonly),
		llb.WithCustomName("Verify image layers match the installed root filesystem"),
		dalec.WithConstraints(splitOpts...),
	).Root()

	return &splitLayersResult{State: merged, Verify: verify, Groups: plan.Groups}, nil
}

// layerSplitFrontend returns the frontend state used to run the layer split
// helpers. The frontend is loaded from a definition, which keeps the cache
// settings it was built with, so its steps are marked to ignore the cache when
// the build ignores all caches.
func layerSplitFrontend(ctx context.Context, client gwclient.Client) (llb.State, error) {
	st := frontend.GetCurrentFrontend(client)

	var c llb.Constraints
	frontend.IgnoreCache(client).SetConstraintsOption(&c)
	if !c.Metadata.IgnoreCache {
		return st, nil
	}

	def, err := st.Marshal(ctx)
	if err != nil {
		return llb.State{}, err
	}
	for dgst, md := range def.Metadata {
		md.IgnoreCache = true
		def.Metadata[dgst] = md
	}
	op, err := llb.NewDefinitionOp(def.ToPB())
	if err != nil {
		return llb.State{}, err
	}
	return llb.NewState(op), nil
}

func solveToState(ctx context.Context, client gwclient.Client, st llb.State, opts ...llb.ConstraintsOpt) (llb.State, error) {
	def, err := st.Marshal(ctx, dalec.WithConstraints(opts...))
	if err != nil {
		return llb.State{}, err
	}
	res, err := client.Solve(ctx, gwclient.SolveRequest{Definition: def.ToPB()})
	if err != nil {
		return llb.State{}, err
	}
	ref, err := res.SingleRef()
	if err != nil {
		return llb.State{}, err
	}
	return ref.ToState()
}

// addLayerHistory appends one image history entry per layer, describing the
// packages it holds.
func addLayerHistory(img *dalec.DockerImageSpec, groups []layersplit.Group) {
	for _, g := range groups {
		img.History = append(img.History, ocispecs.History{CreatedBy: g.HistoryCreatedBy()})
	}
}
