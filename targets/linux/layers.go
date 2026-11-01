package linux

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
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

// imageUpdater is implemented by distros which can update the packages of an
// existing container image instead of installing every package again.
type imageUpdater interface {
	UpdateContainer(ctx context.Context, client gwclient.Client, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, prev llb.State, specPkgs *llb.State, pkgs, keep []string, opts ...llb.ConstraintsOpt) (llb.State, error)
}

// previousImage is a previously built image passed in the
// [dalec.PreviousImageContext] build context.
type previousImage struct {
	State llb.State
	// Config is nil when the context does not carry an image config.
	Config *dockerspec.DockerOCIImage
}

func getPreviousImage(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform) (*previousImage, error) {
	dc, err := dockerui.NewClient(client)
	if err != nil {
		return nil, err
	}
	nc, err := dc.NamedContext(dalec.PreviousImageContext, dockerui.ContextOpt{
		ResolveMode: dc.ImageResolveMode.String(),
		Platform:    platform,
	})
	if err != nil {
		return nil, errors.Wrapf(err, "resolving %s build context", dalec.PreviousImageContext)
	}
	if nc == nil {
		return nil, nil
	}
	st, img, err := nc.Load(ctx)
	if err != nil {
		return nil, errors.Wrapf(err, "loading %s build context", dalec.PreviousImageContext)
	}
	if st == nil {
		return nil, nil
	}
	return &previousImage{State: *st, Config: img}, nil
}

// appPackages returns the spec package names recorded in the layer history of
// a per-package image.
func (p *previousImage) appPackages() []string {
	if p.Config == nil {
		return nil
	}
	var out []string
	for _, h := range p.Config.History {
		if g, ok := layersplit.ParseHistoryComment(h.Comment); ok && g.Kind == layersplit.KindApp {
			out = append(out, g.Packages...)
		}
	}
	return out
}

// specPackageNames returns the names of the packages built from the spec for
// the target (the primary package and its supplemental packages), plus any
// extra names, sorted and without duplicates.
func specPackageNames(spec *dalec.Spec, targetKey string, extra []string) []string {
	names := map[string]bool{spec.Name: true}
	for key, pkg := range spec.Targets[targetKey].Packages {
		names[pkg.ResolvedName(spec.Name, key)] = true
	}
	for _, n := range extra {
		names[n] = true
	}
	out := make([]string, 0, len(names))
	for n := range names {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// updatePackages returns the packages listed in the DALEC_UPDATE_PACKAGES build arg.
func updatePackages(client gwclient.Client) []string {
	v, _ := frontend.GetBuildArg(client, dalec.BuildArgDalecUpdatePackages)
	return strings.FieldsFunc(v, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
}

// updateContainer builds the container root filesystem by upgrading the
// packages of a previous image. It returns the new root filesystem and the
// state to read spec package names from when splitting layers.
func updateContainer(ctx context.Context, client gwclient.Client, sOpt dalec.SourceOpts, c DistroConfig, spec *dalec.Spec, targetKey string, prev *previousImage, prebuilt *llb.State, opts ...llb.ConstraintsOpt) (rootfs, specPkgs llb.State, _ error) {
	updater, ok := c.(imageUpdater)
	if !ok {
		return llb.State{}, llb.State{}, errors.Errorf("updating an existing image is not supported for target %s", targetKey)
	}
	bi, err := spec.GetSingleBase(targetKey)
	if err != nil {
		return llb.State{}, llb.State{}, err
	}
	if bi != nil {
		return llb.State{}, llb.State{}, errors.Errorf("updating an existing image is not supported with a custom base image")
	}

	if prev.Config != nil && prev.Config.Config.Labels[layersplit.LabelImageLayers] != dalec.ImageLayersPerPackage {
		frontend.Warn(ctx, client, prev.State, "the "+dalec.PreviousImageContext+" image was not built with per-package layers, so its layers cannot be reused")
	}

	// The spec's packages are known from the spec itself, plus any recorded
	// in the previous image's layer history (for example if a package was
	// renamed), even when none of them has files of its own.
	names := specPackageNames(spec, targetKey, prev.appPackages())
	if prebuilt != nil {
		specPkgs = *prebuilt
	} else {
		specPkgs = llb.Scratch().File(
			llb.Mkfile(layersplit.AppPackagesFile, 0o644, []byte(strings.Join(names, "\n")+"\n")),
			dalec.WithConstraints(opts...),
		)
	}

	rootfs, err = updater.UpdateContainer(ctx, client, sOpt, spec, targetKey, prev.State, prebuilt, updatePackages(client), names, opts...)
	if err != nil {
		return llb.State{}, llb.State{}, err
	}
	return rootfs, specPkgs, nil
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

	// Solve the installed root filesystem once and use that exact result for
	// every step below. Otherwise steps that ignore the cache (for example
	// with --no-cache-filter) could run the install again between the plan
	// and the final solve, and the split would not match what is verified.
	rootfs, err := solveToState(ctx, client, rootfs, opts...)
	if err != nil {
		return nil, errors.Wrap(err, "building container root filesystem")
	}

	// The split steps always run instead of being loaded from the cache.
	// BuildKit keys execs with read-only mounts by a content checksum that
	// ignores modification times, which could reuse a split of an earlier
	// install whose generated files differ from this one. The steps are fast
	// and deterministic, so unchanged packages still produce identical layers.
	splitOpts := append(slices.Clone(opts), llb.IgnoreCache)

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
		dalec.WithConstraints(splitOpts...),
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
		dalec.WithConstraints(splitOpts...),
	)

	// Each output mount becomes one layer. Unchanged packages produce
	// byte-identical layers because the copy preserves file metadata from the
	// package and normalizes directory timestamps.
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
		llb.WithCustomName("Verify per-package image layers match the installed root filesystem"),
		dalec.WithConstraints(splitOpts...),
	).Root()

	return &splitLayersResult{State: merged, Verify: verify, Groups: plan.Groups}, nil
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
