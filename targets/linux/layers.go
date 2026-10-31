package linux

import (
	"context"
	"strconv"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
	"github.com/project-dalec/dalec/internal/layersplit"
)

// layerSplitter is implemented by distros which can split a container root
// filesystem into per-package layers.
type layerSplitter interface {
	// LayerSplitDumpScript returns a shell script which takes the root
	// filesystem path, the spec packages path and an output path as
	// arguments, and writes the package manager data read by
	// [layersplit.BuildPlan] to the output path.
	LayerSplitDumpScript() string
}

const (
	layerSplitFrontendPath = "/tmp/internal/dalec/layers/frontend"
	layerSplitRootPath     = "/tmp/internal/dalec/layers/rootfs"
	layerSplitPkgsPath     = "/tmp/internal/dalec/layers/pkgs"
	layerSplitPlanPath     = "/tmp/internal/dalec/layers/plan"
	layerSplitOutPath      = "/tmp/internal/dalec/layers/out"
	layerSplitActualPath   = "/tmp/internal/dalec/layers/actual"
)

// imageLayersMode returns the requested image layers mode for the target,
// letting the DALEC_IMAGE_LAYERS build arg override the spec.
func imageLayersMode(client gwclient.Client, spec *dalec.Spec, targetKey string) (string, error) {
	mode := spec.GetImageLayers(targetKey)
	if v, ok := frontend.GetBuildArg(client, dalec.BuildArgDalecImageLayers); ok && v != "" {
		mode = v
	}
	if err := dalec.ValidateImageLayers(mode); err != nil {
		return "", errors.Wrap(err, dalec.BuildArgDalecImageLayers)
	}
	return mode, nil
}

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

// splitLayers splits the installed container root filesystem rootfs into one
// layer per package group plus a final state layer.
func splitLayers(ctx context.Context, client gwclient.Client, worker llb.State, ls layerSplitter, rootfs, pkgs llb.State, opts ...llb.ConstraintsOpt) (*splitLayersResult, error) {
	opts = append(opts, dalec.ProgressGroup("Split image layers"))
	frontendSt := frontend.GetCurrentFrontend(client)

	withFrontend := llb.AddMount(layerSplitFrontendPath, frontendSt, llb.SourcePath("/frontend"), llb.Readonly)
	withRoot := llb.AddMount(layerSplitRootPath, rootfs, llb.Readonly)

	script := ls.LayerSplitDumpScript() + "\n" +
		layerSplitFrontendPath + " " + layersplit.CmdName + " plan" +
		" --root " + layerSplitRootPath +
		" --dump " + layerSplitOutPath +
		" --out " + layerSplitOutPath + "\n"

	planSt := worker.Run(
		llb.Args([]string{"/bin/sh", "-c", script, "layer-split", layerSplitRootPath, layerSplitPkgsPath, layerSplitOutPath}),
		withFrontend,
		withRoot,
		llb.AddMount(layerSplitPkgsPath, pkgs, llb.Readonly),
		llb.WithCustomName("Plan per-package image layers"),
		dalec.WithConstraints(opts...),
	).AddMount(layerSplitOutPath, llb.Scratch())

	def, err := planSt.Marshal(ctx, dalec.WithConstraints(opts...))
	if err != nil {
		return nil, err
	}
	res, err := client.Solve(ctx, gwclient.SolveRequest{Definition: def.ToPB()})
	if err != nil {
		return nil, errors.Wrap(err, "planning per-package image layers")
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

	// Directory timestamps in layers are normalized so identical packages
	// produce identical layers across builds.
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
		llb.WithCustomName("Copy files into per-package image layers"),
		dalec.WithConstraints(opts...),
	)

	// Each output mount becomes one layer. Unchanged packages produce
	// byte-identical layers because the copy preserves file metadata from the
	// package and normalizes directory timestamps.
	//
	// Note: the layers must not be passed through a separate file op copy to
	// get content-based cache keys per layer. BuildKit's content checksum
	// ignores modification times, so a per-layer cache hit could reuse a layer
	// whose package file timestamps differ from the installed root
	// filesystem. The exec above is keyed by the whole root filesystem,
	// including the package database, which records package file timestamps.
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
			"--plan", layerSplitPlanPath,
		}),
		withFrontend,
		withRoot,
		llb.AddMount(layerSplitPlanPath, planSt, llb.Readonly),
		llb.AddMount(layerSplitActualPath, merged, llb.Readonly),
		llb.WithCustomName("Verify per-package image layers match the installed root filesystem"),
		dalec.WithConstraints(opts...),
	).Root()

	return &splitLayersResult{State: merged, Verify: verify, Groups: plan.Groups}, nil
}

// addLayerHistory appends one image history entry per layer group, so tools
// can tell which packages each layer holds.
func addLayerHistory(img *dalec.DockerImageSpec, groups []layersplit.Group) {
	for _, g := range groups {
		img.History = append(img.History, ocispecs.History{
			CreatedBy: g.HistoryCreatedBy(),
			Comment:   g.HistoryComment(),
		})
	}
	if img.Config.Labels == nil {
		img.Config.Labels = make(map[string]string)
	}
	img.Config.Labels[layersplit.LabelImageLayers] = dalec.ImageLayersPerPackage
}
