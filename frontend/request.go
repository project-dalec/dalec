package frontend

import (
	"context"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	"github.com/pkg/errors"
	"github.com/project-dalec/dalec"
)

const (
	keySkipSigningArg                     = "DALEC_SKIP_SIGNING"
	buildArgDalecSigningConfigPath        = "DALEC_SIGNING_CONFIG_PATH"
	buildArgDalecSigningConfigContextName = "DALEC_SIGNING_CONFIG_CONTEXT_NAME"
)

type solveRequestOpt func(*gwclient.SolveRequest) error

func newSolveRequest(opts ...solveRequestOpt) (gwclient.SolveRequest, error) {
	var sr gwclient.SolveRequest

	for _, o := range opts {
		if err := o(&sr); err != nil {
			return sr, err
		}
	}
	return sr, nil
}

func toFrontend(f *dalec.Frontend) solveRequestOpt {
	return func(req *gwclient.SolveRequest) error {
		req.Frontend = gatewayFrontend
		if req.FrontendOpt == nil {
			req.FrontendOpt = make(map[string]string)
		}
		req.FrontendOpt["source"] = f.Image
		req.FrontendOpt["cmdline"] = f.CmdLine
		return nil
	}
}

func withSpec(ctx context.Context, spec *dalec.Spec, opts ...llb.ConstraintsOpt) solveRequestOpt {
	return func(req *gwclient.SolveRequest) error {
		if req.FrontendInputs == nil {
			req.FrontendInputs = make(map[string]*pb.Definition)
		}

		dt, err := yaml.Marshal(spec)
		if err != nil {
			return errors.Wrap(err, "error marshalling spec to yaml")
		}

		def, err := llb.Scratch().File(llb.Mkfile(dockerui.DefaultDockerfileName, 0600, dt), opts...).Marshal(ctx)
		if err != nil {
			return errors.Wrap(err, "error marshaling spec to LLB")
		}
		req.FrontendInputs[dockerui.DefaultLocalNameDockerfile] = def.ToPB()
		return nil
	}
}

func withTarget(t string) solveRequestOpt {
	return func(req *gwclient.SolveRequest) error {
		if req.FrontendOpt == nil {
			req.FrontendOpt = make(map[string]string)
		}
		req.FrontendOpt["target"] = t
		return nil
	}
}

func withBuildArgs(args map[string]string) solveRequestOpt {
	return func(req *gwclient.SolveRequest) error {
		if len(args) == 0 {
			return nil
		}

		if req.FrontendOpt == nil {
			req.FrontendOpt = make(map[string]string)
		}
		for k, v := range args {
			req.FrontendOpt["build-arg:"+k] = v
		}
		return nil
	}
}

func toDockerfile(ctx context.Context, bctx llb.State, dt []byte, spec *dalec.SourceBuild, opts ...llb.ConstraintsOpt) solveRequestOpt {
	return func(req *gwclient.SolveRequest) error {
		req.Frontend = "dockerfile.v0"

		bctxDef, err := bctx.Marshal(ctx)
		if err != nil {
			return errors.Wrap(err, "error marshaling dockerfile to LLB")
		}
		if req.FrontendInputs == nil {
			req.FrontendInputs = make(map[string]*pb.Definition)
		}

		dfDef, err := marshalDockerfile(ctx, dt, opts...)
		if err != nil {
			return errors.Wrap(err, "error marshaling dockerfile to LLB")
		}

		req.FrontendInputs[dockerui.DefaultLocalNameContext] = bctxDef.ToPB()
		req.FrontendInputs[dockerui.DefaultLocalNameDockerfile] = dfDef.ToPB()

		if ref, cmdline, _, ok := parser.DetectSyntax(dt); ok {
			req.Frontend = gatewayFrontend
			if req.FrontendOpt == nil {
				req.FrontendOpt = make(map[string]string)
			}
			req.FrontendOpt["source"] = ref
			req.FrontendOpt["cmdline"] = cmdline
		}

		if spec != nil {
			if req.FrontendOpt == nil {
				req.FrontendOpt = make(map[string]string)
			}
			if spec.Target != "" {
				req.FrontendOpt["target"] = spec.Target
			}
			for k, v := range spec.Args {
				req.FrontendOpt["build-arg:"+k] = v
			}
		}
		return nil
	}
}

func marshalDockerfile(ctx context.Context, dt []byte, opts ...llb.ConstraintsOpt) (*llb.Definition, error) {
	st := llb.Scratch().File(llb.Mkfile(dockerui.DefaultDockerfileName, 0600, dt), opts...)
	return st.Marshal(ctx)
}

func getSigningConfigFromContext(ctx context.Context, client gwclient.Client, cfgPath string, configCtxName string, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) (*dalec.PackageSigner, error) {
	src := dalec.Source{Path: cfgPath, Context: &dalec.SourceContext{Name: configCtxName}}
	signConfigState := src.ToState("", sOpt, opts...)

	scDef, err := signConfigState.Marshal(ctx)
	if err != nil {
		return nil, err
	}

	res, err := client.Solve(ctx, gwclient.SolveRequest{
		Definition: scDef.ToPB(),
	})
	if err != nil {
		return nil, err
	}

	ref, err := res.SingleRef()
	if err != nil {
		return nil, err
	}

	dt, err := ref.ReadFile(ctx, gwclient.ReadRequest{
		Filename: cfgPath,
	})
	if err != nil {
		return nil, err
	}

	var pc dalec.PackageConfig
	if err := yaml.Unmarshal(dt, &pc); err != nil {
		return nil, err
	}

	return pc.Signer, nil
}

func MaybeSign(ctx context.Context, client gwclient.Client, st llb.State, spec *dalec.Spec, targetKey string, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) llb.State {
	if signingDisabled(client) {
		Warnf(ctx, client, st, "Signing disabled by build-arg %q", keySkipSigningArg)
		return st
	}

	cfg, rootSigningSpecOverriddenByTarget := spec.GetSigner(targetKey)
	cfgPath := getUserSignConfigPath(client)
	if cfgPath == "" {
		if cfg == nil {
			// i.e. there's no signing config. not in the build context, not in the spec.
			return st
		}

		if rootSigningSpecOverriddenByTarget {
			Warnf(ctx, client, st, "Root signing spec overridden by target signing spec: target %q", targetKey)
		}

		return dalec.ErrorState(forwardToSigner(ctx, client, cfg, st, opts...))
	}

	configCtxName := getSignContextNameWithDefault(client)
	if specCfg := cfg; specCfg != nil {
		Warnf(ctx, client, st, "Spec signing config overwritten by config at path %q in build-context %q", cfgPath, configCtxName)
	}

	cfg, err := getSigningConfigFromContext(ctx, client, cfgPath, configCtxName, sOpt)
	if err != nil {
		return dalec.ErrorState(llb.Scratch(), err)
	}

	return dalec.ErrorState(forwardToSigner(ctx, client, cfg, st, opts...))
}

func getSignContextNameWithDefault(client gwclient.Client) string {
	configCtxName := dockerui.DefaultLocalNameContext
	if cn := getSignConfigCtxName(client); cn != "" {
		configCtxName = cn
	}
	return configCtxName
}

func signingDisabled(client gwclient.Client) bool {
	bopts := client.BuildOpts().Opts
	v, ok := bopts["build-arg:"+keySkipSigningArg]
	if !ok {
		return false
	}

	isDisabled, err := strconv.ParseBool(v)
	if err != nil {
		return false
	}

	return isDisabled
}

func getUserSignConfigPath(client gwclient.Client) string {
	return client.BuildOpts().Opts["build-arg:"+buildArgDalecSigningConfigPath]
}

func getSignConfigCtxName(client gwclient.Client) string {
	return client.BuildOpts().Opts["build-arg:"+buildArgDalecSigningConfigContextName]
}

func forwardToSigner(ctx context.Context, client gwclient.Client, cfg *dalec.PackageSigner, s llb.State, opts ...llb.ConstraintsOpt) (llb.State, error) {
	const (
		// See https://github.com/moby/buildkit/blob/d8d946b85c52095d34a52ce210960832f4e06775/frontend/dockerui/context.go#L29
		contextKey = "contextkey"
	)

	bopts := client.BuildOpts().Opts

	req, err := newSolveRequest(toFrontend(cfg.Frontend), withBuildArgs(cfg.Args))
	if err != nil {
		return llb.Scratch(), err
	}

	for k, v := range bopts {
		if k == "source" || k == "cmdline" {
			continue
		}
		req.FrontendOpt[k] = v
	}

	inputs, err := client.Inputs(ctx)
	if err != nil {
		return llb.Scratch(), err
	}

	m := make(map[string]*pb.Definition)

	for k, st := range inputs {
		def, err := st.Marshal(ctx)
		if err != nil {
			return llb.Scratch(), err
		}
		m[k] = def.ToPB()
	}
	req.FrontendInputs = m

	opts = append(opts, dalec.ProgressGroup("Sign package"))
	stateDef, err := s.Marshal(ctx, opts...)
	if err != nil {
		return llb.Scratch(), err
	}

	req.FrontendOpt[contextKey] = dockerui.DefaultLocalNameContext
	req.FrontendInputs[dockerui.DefaultLocalNameContext] = stateDef.ToPB()
	req.FrontendOpt["dalec.target"] = bopts["dalec.target"]

	res, err := client.Solve(ctx, req)
	if err != nil {
		return llb.Scratch(), errors.Wrap(err, "error signing packages")
	}

	ref, err := res.SingleRef()
	if err != nil {
		return llb.Scratch(), err
	}

	return ref.ToState()
}

// Returns a ConstraintsOpt that ignores the cache if the client
// is configured to ignore the cache for any of the given refs OR
// if the global no-cache option is set.
func IgnoreCache(client gwclient.Client, refs ...string) llb.ConstraintsOpt {
	const keyNoCache = "no-cache"

	opts := client.BuildOpts().Opts
	v, ok := opts[keyNoCache]
	if !ok {
		// no-cache not set
		return dalec.ConstraintsOptFunc(func(c *llb.Constraints) {})
	}

	if v == "" {
		// global no-cache, ignore regardless of refs
		return llb.IgnoreCache
	}

	idx := make(map[string]struct{}, len(refs))

	for _, ref := range refs {
		idx[ref] = struct{}{}
	}

	for filter := range strings.SplitSeq(v, ",") {
		if _, ok := idx[filter]; ok {
			return llb.IgnoreCache
		}
	}

	return dalec.ConstraintsOptFunc(func(c *llb.Constraints) {})
}
