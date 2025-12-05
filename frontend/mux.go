package frontend

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/project-dalec/dalec"
	"github.com/containerd/platforms"
	"github.com/goccy/go-yaml"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests"
	bktargets "github.com/moby/buildkit/frontend/subrequests/targets"
	"github.com/moby/buildkit/util/bklog"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"golang.org/x/exp/maps"
)

const (
	keyResolveSpec = "frontend.dalec.resolve"

	// KeyDefaultPlatform is the subreuqest id for returning the default platform
	// for the builder.
	KeyDefaultPlatform = "frontend.dalec.defaultPlatform"
)

// BuildMux implements a buildkit BuildFunc via its Handle method. With a
// BuildMux you register routes with mux.Add("someKey", SomeHandler,
// optionalTargetInfo).
//
// When a build request is made, BuildMux tries to match the requested build
// target to a registered handler via the registered handler. The correct
// handler to use is determined with the following logic:
//
//  1. Build target is an exact match to a registered route
//  2. Build target is empty, check if a default handler is registered
//  3. Check if any of the registered
//     handlers have a route that is a prefix match to the build target

// BuildMux route handlers also must be buildkit BuildFuncs. As such a handler
// can itself also be a distinc BuildMux with its own set of routes.  This
// allows handlers to also do their own routing. All logic for what to route is
// handled outside of the BuildMux.
//
// BuildMux and buildkit BuildFunc have a similar relationship as http.ServeMux
// and http.Handler (where http.ServeMux is an http.Handler).  In the same way
// BuildMux is a BuildFunc (or rather BuildMux.Handle is).  So BuildMux can be
// nested.
//
// When BuildMux calls a handler, it modifies the client to chomp off the
// matched route prefix.  So a BuildMux with receiving a build target of
// mariner2/container will match on the registered handler for mariner2 then
// call the handler with the build target changed to just container.
//
// Finally, BuildMux sets an extra build option on the client
// dalec.target=<matched prefix>.  This is only done if the dalec.target option
// is not already set, so dalec.target is only modified once and then used in
// handlers to determine which target in spec.Targets applies to them.
type BuildMux struct {
	handlers map[string]handler
	defaultH *handler
	// cached spec so we don't have to load it every time its needed
	spec *dalec.Spec
}

type handler struct {
	f gwclient.BuildFunc
	t *bktargets.Target
}

// Add adds a handler for the given target
// [targetPath] is the resource path to be handled
func (m *BuildMux) Add(targetPath string, bf gwclient.BuildFunc, info *bktargets.Target) {
	if m.handlers == nil {
		m.handlers = make(map[string]handler)
	}

	h := handler{bf, info}
	m.handlers[targetPath] = h

	if info != nil && info.Default {
		m.defaultH = &h
	}

	bklog.G(context.TODO()).WithField("target", targetPath).Info("Added handler to router")
}

const keyTarget = "target"

// describe returns the subrequests that are supported
func (m *BuildMux) describe() (*gwclient.Result, error) {
	subs := []subrequests.Request{bktargets.SubrequestsTargetsDefinition, subrequests.SubrequestsDescribeDefinition}

	dt, err := json.Marshal(subs)
	if err != nil {
		return nil, errors.Wrap(err, "error marshalling describe result to json")
	}

	buf := bytes.NewBuffer(nil)
	if err := subrequests.PrintDescribe(dt, buf); err != nil {
		return nil, err
	}

	res := gwclient.NewResult()
	res.Metadata = map[string][]byte{
		"result.txt": buf.Bytes(),
		"version":    []byte(subrequests.SubrequestsDescribeDefinition.Version),
	}
	return res, nil
}

func (m *BuildMux) handleSubrequest(ctx context.Context, client gwclient.Client, opts map[string]string) (*gwclient.Result, bool, error) {
	switch opts[requestIDKey] {
	case "":
		return nil, false, nil
	case subrequests.RequestSubrequestsDescribe:
		res, err := m.describe()
		return res, true, err
	case bktargets.SubrequestsTargetsDefinition.Name:
		res, err := m.list(ctx, client, opts[keyTarget])
		return res, true, err
	// case keyTopLevelTarget:
	//	return nil, false, nil
	case keyResolveSpec:
		res, err := handleResolveSpec(ctx, client)
		return res, true, err
	case KeyDefaultPlatform:
		res, err := handleDefaultPlatform()
		return res, true, err
	default:
		return nil, false, errors.Errorf("unsupported subrequest %q", opts[requestIDKey])
	}
}

func handleResolveSpec(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	dc, err := dockerui.NewClient(client)
	if err != nil {
		return nil, err
	}

	targets := dc.TargetPlatforms
	if len(targets) == 0 {
		targets = append(targets, platforms.DefaultSpec())
	}

	out := make([]*dalec.Spec, 0, len(targets))
	for _, p := range targets {
		spec, err := LoadSpec(ctx, dc, &p)
		if err != nil {
			return nil, err
		}
		out = append(out, spec)
	}

	dtYaml, err := yaml.Marshal(out)
	if err != nil {
		return nil, err
	}

	dtJSON, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}

	res := gwclient.NewResult()
	res.AddMeta("result.json", dtJSON)
	// result.txt here so that `docker buildx build --print` will output it directly.
	// Otherwise it prints a go object.
	res.AddMeta("result.txt", dtYaml)

	return res, nil
}

// handleDefaultPlatform returns the default platform
func handleDefaultPlatform() (*gwclient.Result, error) {
	res := gwclient.NewResult()

	p := platforms.DefaultSpec()
	dt, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}

	res.AddMeta("result.json", dt)
	res.AddMeta("result.txt", []byte(platforms.Format(p)))

	return res, nil
}

func (m *BuildMux) loadSpec(ctx context.Context, client gwclient.Client) (*dalec.Spec, error) {
	if m.spec != nil {
		return m.spec, nil
	}
	dc, err := dockerui.NewClient(client)
	if err != nil {
		return nil, err
	}

	// Note: this is not suitable for passing to builds since it does not have platform information
	spec, err := LoadSpec(ctx, dc, nil, func(cfg *LoadConfig) {
		// We want to allow any arg to be passed to the spec since we don't know what
		// args are valid at this point, nor do we care here.
		cfg.SubstituteOpts = append(cfg.SubstituteOpts, dalec.WithAllowAnyArg)
	})
	if err != nil {
		return nil, err
	}
	m.spec = spec

	return spec, nil
}

func maybeSetDalecTargetKey(client gwclient.Client, key string) gwclient.Client {
	opts := client.BuildOpts()
	if opts.Opts[keyTopLevelTarget] != "" {
		// do nothing since this is already set
		return client
	}

	// optimization to help prevent unnecessary grpc requests
	// The gateway client will make a grpc request to get the build opts from the gateway.
	// This just caches those opts locally.
	// If the client is already a clientWithCustomOpts, then the opts are already cached.
	if _, ok := client.(*clientWithCustomOpts); !ok {
		// this forces the client to use our cached opts from above
		client = &clientWithCustomOpts{opts: opts, Client: client}
	}
	return setClientOptOption(client, map[string]string{keyTopLevelTarget: key, "build-arg:" + dalec.KeyDalecTarget: key})
}

// list outputs the list of targets that are supported by the mux
func (m *BuildMux) list(ctx context.Context, client gwclient.Client, target string) (*gwclient.Result, error) {
	var ls bktargets.List

	var check []string
	if target == "" {
		check = maps.Keys(m.handlers)
	} else {
		// Use the target as a filter so the response only includes routes that are underneath the target
		check = append(check, target)
	}

	slices.Sort(check)

	bklog.G(ctx).WithField("checks", check).Debug("Checking targets")

	for _, t := range check {
		ctx := bklog.WithLogger(ctx, bklog.G(ctx).WithField("check", t))
		bklog.G(ctx).Debug("Lookup target")
		matched, h, err := m.lookupTarget(ctx, t)
		if err != nil {
			bklog.G(ctx).WithError(err).Warn("Error looking up target, skipping")
			continue
		}

		ctx = bklog.WithLogger(ctx, bklog.G(ctx).WithField("matched", matched))
		bklog.G(ctx).Debug("Matched target")

		if h.t != nil {
			t := *h.t
			// We have the target info, we can use this directly
			ls.Targets = append(ls.Targets, t)
			continue
		}

		bklog.G(ctx).Info("No target info, calling handler")
		// No target info, so call the handler to get the info
		// This calls the route handler.
		// The route handler must be setup to handle the subrequest
		// Today we assume all route handers are setup to handle the subrequest.
		res, err := h.f(ctx, maybeSetDalecTargetKey(trimTargetOpt(client, matched), matched))
		if err != nil {
			bklog.G(ctx).Errorf("%+v", err)
			return nil, err
		}

		var _ls bktargets.List
		if err := unmarshalResult(res, &_ls); err != nil {
			return nil, err
		}

		for _, t := range _ls.Targets {
			t.Name = path.Join(matched, t.Name)
			ls.Targets = append(ls.Targets, t)
		}
	}

	return ls.ToResult()
}

type noSuchHandlerError struct {
	Target    string
	Available []string
}

func handlerNotFound(target string, available []string) error {
	return &noSuchHandlerError{Target: target, Available: available}
}

func (err *noSuchHandlerError) Error() string {
	return fmt.Sprintf("no such handler for target %q: available targets: %s", err.Target, strings.Join(err.Available, ", "))
}

func (m *BuildMux) lookupTarget(ctx context.Context, target string) (matchedPattern string, _ *handler, _ error) {
	// `target` is from `docker build --target=<target>`
	// cases for `t` are as follows:
	//    1. may have an exact match in the handlers (ideal)
	//    2. No matching handler and `target == ""` and there is a default handler set (assume default handler)
	//    3. may have a prefix match in the handlers, e.g. handler for `foo`, `target == "foo/bar"` (assume nested route)
	//    4. No match in the handlers (error)
	h, ok := m.handlers[target]
	if ok {
		return target, &h, nil
	}

	if target == "" && m.defaultH != nil {
		bklog.G(ctx).Info("Using default target")
		return target, m.defaultH, nil
	}

	var candidates []string
	for k := range m.handlers {
		// for prefix matching, we should skip anything that doesn't have a target defined
		if strings.HasPrefix(target, k+"/") {
			candidates = append(candidates, k)
		}
	}

	if len(candidates) > 0 {
		// Sort uses a lexicographic sort, so the longest prefix will be last
		// We want to match the longest prefix, so we need the last element
		slices.Sort(candidates)
		k := candidates[len(candidates)-1]
		h := m.handlers[k]
		bklog.G(ctx).WithField("prefix", k).WithField("matching request", target).Info("Using prefix match for target")
		return k, &h, nil
	}

	return "", nil, handlerNotFound(target, maps.Keys(m.handlers))
}

// Handle is a [gwclient.BuildFunc] that routes requests to registered handlers
func (m *BuildMux) Handle(ctx context.Context, client gwclient.Client) (_ *gwclient.Result, retErr error) {
	// Cache the opts in case this is the raw client
	// This prevents a grpc request for multiple calls to BuildOpts
	opts := client.BuildOpts().Opts
	origOpts := dalec.DuplicateMap(opts)

	t := opts[keyTarget]

	defer func() {
		if retErr != nil {
			if _, ok := origOpts[keyTopLevelTarget]; !ok {
				retErr = errors.Wrapf(retErr, "error handling requested build target %q", t)

				// If we have a spec name, load it to make the error message more helpful
				spec, _ := m.loadSpec(ctx, client)
				if spec != nil && spec.Name != "" {
					retErr = errors.Wrapf(retErr, "spec: %s", spec.Name)
				}
			}
		}
	}()

	ctx = bklog.WithLogger(ctx, bklog.G(ctx).
		WithFields(logrus.Fields{
			"handlers":  maps.Keys(m.handlers),
			"target":    opts[keyTarget],
			"requestid": opts[requestIDKey],
			"targetKey": GetTargetKey(client),
		}))

	bklog.G(ctx).Info("Handling request")

	res, handled, err := m.handleSubrequest(ctx, client, opts)
	if err != nil {
		return nil, err
	}
	if handled {
		return res, nil
	}

	matched, h, err := m.lookupTarget(ctx, t)
	if err != nil {
		return nil, err
	}

	ctx = bklog.WithLogger(ctx, bklog.G(ctx).WithField("matched", matched))

	// each call to `Handle` handles the next part of the target
	// When we call the handler, we want to remove the part of the target that is being handled so the next handler can handle the next part
	client = trimTargetOpt(client, matched)
	client = maybeSetDalecTargetKey(client, matched)

	res, err = h.f(ctx, client)
	if err != nil {
		err = injectPathsToNotFoundError(matched, err)
		return res, err
	}

	// If this request was a request to list targets, we need to modify the response a bit
	// Otherwise we can just return the result as is.
	if opts[requestIDKey] == bktargets.SubrequestsTargetsDefinition.Name {
		return m.fixupListResult(matched, res)
	}
	return res, nil
}

// fixupListResult updates the targets to include the matched key in their path
// This is used when a list request is made.
//
// The target handler does not know know the full path of the target.
// Specifically it does not include the `matched` part of the target path
// because the matched part is removed from the target path before the handler
// is called.
// This function adds the matched part back to the target path so the response includes the full path.
func (m *BuildMux) fixupListResult(matched string, res *gwclient.Result) (*gwclient.Result, error) {
	var v bktargets.List
	if err := unmarshalResult(res, &v); err != nil {
		return nil, err
	}

	updated := make([]bktargets.Target, 0, len(v.Targets))
	for _, t := range v.Targets {
		t.Name = path.Join(matched, t.Name)
		updated = append(updated, t)
	}

	v.Targets = updated
	if err := marshalResult(res, &v); err != nil {
		return nil, err
	}

	asResult, err := v.ToResult()
	if err != nil {
		return nil, err
	}

	// update the original result with the new data
	// See `v.ToResult()` for the metadata keys
	res.AddMeta("result.json", asResult.Metadata["result.json"])
	res.AddMeta("result.txt", asResult.Metadata["result.txt"])
	res.AddMeta("version", asResult.Metadata["version"])
	return res, nil
}

// If the error is from noSuchHandlerError, we want to update the error to include the matched target
// This makes sure the returned error message has the full target path.
func injectPathsToNotFoundError(matched string, err error) error {
	if err == nil {
		return nil
	}

	var e *noSuchHandlerError
	if !errors.As(err, &e) {
		return err
	}

	e.Target = path.Join(matched, e.Target)
	for i, v := range e.Available {
		e.Available[i] = path.Join(matched, v)
	}
	return e
}

func unmarshalResult[T any](res *gwclient.Result, v *T) error {
	dt, ok := res.Metadata["result.json"]
	if !ok {
		return errors.Errorf("no result.json metadata in response")
	}
	return json.Unmarshal(dt, v)
}

func marshalResult[T any](res *gwclient.Result, v *T) error {
	dt, err := json.Marshal(v)
	if err != nil {
		return errors.Wrap(err, "error marshalling result to json")
	}
	res.Metadata["result.json"] = dt
	res.Metadata["result.txt"] = dt
	return nil
}

// CurrentFrontend is an interface typically implemented by a [gwclient.Client]
// This is used to get the rootfs of the current frontend.
type CurrentFrontend interface {
	CurrentFrontend() (*llb.State, error)
}

var (
	_ gwclient.Client = (*clientWithCustomOpts)(nil)
	_ CurrentFrontend = (*clientWithCustomOpts)(nil)
)

type clientWithCustomOpts struct {
	opts gwclient.BuildOpts
	gwclient.Client
}

func trimTargetOpt(client gwclient.Client, prefix string) *clientWithCustomOpts {
	opts := client.BuildOpts()

	updated := strings.TrimPrefix(opts.Opts[keyTarget], prefix)
	if len(updated) > 0 && updated[0] == '/' {
		updated = updated[1:]
	}
	opts.Opts[keyTarget] = updated
	return &clientWithCustomOpts{
		Client: client,
		opts:   opts,
	}
}

func setClientOptOption(client gwclient.Client, extraOpts map[string]string) *clientWithCustomOpts {
	opts := client.BuildOpts()

	for key, value := range extraOpts {
		opts.Opts[key] = value
	}
	return &clientWithCustomOpts{
		Client: client,
		opts:   opts,
	}
}

func (d *clientWithCustomOpts) BuildOpts() gwclient.BuildOpts {
	return d.opts
}
func (d *clientWithCustomOpts) CurrentFrontend() (*llb.State, error) {
	return d.Client.(CurrentFrontend).CurrentFrontend()
}

// Handler returns a [gwclient.BuildFunc] that uses the mux to route requests to appropriate handlers
func (m *BuildMux) Handler(opts ...func(context.Context, gwclient.Client, *BuildMux) error) gwclient.BuildFunc {
	return func(ctx context.Context, client gwclient.Client) (_ *gwclient.Result, retErr error) {
		defer func() {
			defer func() {
				if r := recover(); r != nil {
					trace := getPanicStack()
					recErr := fmt.Errorf("recovered from panic in handler: %+v", r)
					retErr = stderrors.Join(recErr, trace)
				}
			}()

		}()

		if !SupportsDiffMerge(client) {
			dalec.DisableDiffMerge(true)
		}
		for _, opt := range opts {
			if err := opt(ctx, client, m); err != nil {
				return nil, err
			}
		}
		return m.Handle(ctx, client)
	}
}

// WithTargetForwardingHandler registers a handler for each spec target that has a custom frontend
func WithTargetForwardingHandler(ctx context.Context, client gwclient.Client, m *BuildMux) error {
	if k := GetTargetKey(client); k != "" {
		// This is already a forwarded request, so we don't want to forward again
		return fmt.Errorf("target forwarding requested but target is already forwarded: this is a bug in the frontend for %q", k)
	}
	spec, err := m.loadSpec(ctx, client)
	if err != nil {
		return err
	}

	for key, t := range spec.Targets {
		if t.Frontend == nil {
			continue
		}

		m.Add(key, func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
			ctx = bklog.WithLogger(ctx, bklog.G(ctx).WithField("frontend", key).WithField("frontend-ref", t.Frontend.Image).WithField("forwarded", true))
			bklog.G(ctx).Info("Forwarding to custom frontend")
			req, err := newSolveRequest(
				copyForForward(ctx, client),
				withSpec(ctx, spec, dalec.ProgressGroup("prepare spec to forward to frontend")),
				toFrontend(t.Frontend),
				withTarget(client.BuildOpts().Opts[keyTarget]),
			)

			if err != nil {
				return nil, err
			}

			return client.Solve(ctx, req)
		}, nil)
		bklog.G(ctx).WithField("target", key).WithField("targets", maps.Keys(m.handlers)).WithField("targetKey", GetTargetKey(client)).Info("Added custom frontend to router")
	}
	return nil
}

// WithBuiltinHandler registers a late-binding handler for the given target key.
// These are only added if the target is in the spec OR the spec has no explicit targets.
func WithBuiltinHandler(key string, bf gwclient.BuildFunc) func(context.Context, gwclient.Client, *BuildMux) error {
	return func(ctx context.Context, client gwclient.Client, m *BuildMux) error {
		if !shouldLoadTarget(ctx, client, m, key) {
			return nil
		}
		m.Add(key, bf, nil)
		return nil
	}
}

// shouldLoadTarget is used to determine if the spec is overriding the built-in
// target with the same targetKey.
//
// When there is nothing specified in `spec.Targets` this always returns true.
//
// When `spec.Targets` is populated but the provided targetKey does not appear
// in `spec.Targets` this returns false.
//
// When the provided targetKey is in `spec.Targets` but the target spec defines
// a frontend to forward to, this returns false.
//
// Otherwise true.
func shouldLoadTarget(ctx context.Context, client gwclient.Client, mux *BuildMux, targetKey string) bool {
	spec, err := mux.loadSpec(ctx, client)
	if err != nil {
		Warnf(ctx, client, llb.Scratch(), "Cannot load target %s due to error loading spec: %v", targetKey, err)
		return false
	}

	if len(spec.Targets) == 0 {
		return true
	}

	t, ok := spec.Targets[targetKey]
	if !ok {
		bklog.G(ctx).WithField("spec targets", maps.Keys(spec.Targets)).WithField("targetKey", targetKey).Info("Target not in the spec, skipping")
		return false
	}

	if t.Frontend != nil {
		bklog.G(ctx).WithField("targetKey", targetKey).Info("Target has custom frontend, skipping builtin-handler")
		Warnf(ctx, client, llb.Scratch(), "Built-in target %q overwritten by target in spec", targetKey)
		return false
	}

	return true
}

// LoadBuiltinTargets is like [WithBuiltinHandler] but accepts a mapping of handlers
// instead of one at a time.
func LoadBuiltinTargets(targets map[string]gwclient.BuildFunc) func(context.Context, gwclient.Client, *BuildMux) error {
	return func(ctx context.Context, client gwclient.Client, mux *BuildMux) error {
		for target, handler := range targets {
			if shouldLoadTarget(ctx, client, mux, target) {
				mux.Add(target, handler, nil)
			}
		}
		return nil
	}
}
