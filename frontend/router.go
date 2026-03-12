package frontend

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests"
	bktargets "github.com/moby/buildkit/frontend/subrequests/targets"
	"github.com/moby/buildkit/util/bklog"
	"github.com/pkg/errors"
	"github.com/project-dalec/dalec"
	"github.com/sirupsen/logrus"
	"golang.org/x/exp/maps"
)

// Target wraps bktargets.Target with dalec-specific metadata
// for richer target list responses.
type Target struct {
	bktargets.Target

	// SpecDefined indicates that the top-level target key for this route
	// appears in spec.Targets. When spec.Targets is empty (no explicit
	// target filtering), this field is false for all targets.
	SpecDefined bool `json:"specDefined,omitempty"`

	// Hidden indicates that this route should not appear in the target
	// list but is still dispatchable. This is used for routes like bare
	// distro names (e.g. "mariner2") that act as aliases for a default
	// sub-route (e.g. "mariner2/container").
	Hidden bool `json:"hidden,omitempty"`
}

// TargetList is the dalec-extended target list response.
type TargetList struct {
	Targets []Target `json:"targets"`
}

// Route is a single entry in the flat router.
type Route struct {
	// FullPath is the fully qualified route path, e.g. "azlinux3/container".
	FullPath string

	// Handler is the build function that handles this route.
	Handler gwclient.BuildFunc

	// Info contains target metadata for the target list API.
	Info Target

	// Forward, if set, indicates this route forwards to an external frontend.
	// The target list queries the remote frontend lazily to discover sub-targets.
	Forward *Forward
}

// Forward holds the configuration needed to query a forwarded frontend's
// target list at list time.
type Forward struct {
	Spec     *dalec.Spec
	Frontend *dalec.Frontend
}

// Router is a flat route table where all routes are fully qualified paths.
// It replaces the hierarchical BuildMux with a simpler dispatch model.
type Router struct {
	routes map[string]Route

	// cached spec so we don't have to load it every time its needed
	spec *dalec.Spec
}

// Add registers a route. If a route with the same FullPath already exists
// it is overwritten (this is used by target forwarding to override builtins).
func (r *Router) Add(ctx context.Context, route Route) {
	if r.routes == nil {
		r.routes = make(map[string]Route)
	}
	r.routes[route.FullPath] = route
	bklog.G(ctx).WithField("route", route.FullPath).Debug("Added route to router")
}

// Handler returns a gwclient.BuildFunc that dispatches requests through the router.
// Option functions run once at the start of each request before dispatch.
func (r *Router) Handler(opts ...func(context.Context, gwclient.Client, *Router) error) gwclient.BuildFunc {
	return func(ctx context.Context, client gwclient.Client) (_ *gwclient.Result, retErr error) {
		defer func() {
			if rec := recover(); rec != nil {
				trace := getPanicStack()
				recErr := fmt.Errorf("recovered from panic in handler: %+v", rec)
				retErr = stderrors.Join(recErr, trace)
			}
		}()

		if !SupportsDiffMerge(client) {
			dalec.DisableDiffMerge(true)
		}

		for _, opt := range opts {
			if err := opt(ctx, client, r); err != nil {
				return nil, err
			}
		}

		return r.Handle(ctx, client)
	}
}

// Handle is the main dispatch function. It handles subrequests, then
// looks up the target in the route table and calls the matched handler.
func (r *Router) Handle(ctx context.Context, client gwclient.Client) (_ *gwclient.Result, retErr error) {
	opts := client.BuildOpts().Opts
	target := opts[keyTarget]

	defer func() {
		if retErr != nil {
			if _, ok := opts[keyTopLevelTarget]; !ok {
				retErr = errors.Wrapf(retErr, "error handling requested build target %q", target)

				spec, _ := r.loadSpec(ctx, client)
				if spec != nil && spec.Name != "" {
					retErr = errors.Wrapf(retErr, "spec: %s", spec.Name)
				}
			}
		}
	}()

	ctx = bklog.WithLogger(ctx, bklog.G(ctx).
		WithFields(logrus.Fields{
			"target":    target,
			"requestid": opts[requestIDKey],
			"targetKey": GetTargetKey(client),
		}))

	bklog.G(ctx).Info("Handling request")

	res, handled, err := r.handleSubrequest(ctx, client, opts)
	if err != nil {
		return nil, err
	}
	if handled {
		return res, nil
	}

	matched, route, err := r.lookupTarget(ctx, target)
	if err != nil {
		return nil, err
	}

	ctx = bklog.WithLogger(ctx, bklog.G(ctx).WithField("matched", matched))

	// Set the top-level target key on the client.
	client = maybeSetDalecTargetKey(client, topLevelKey(matched))

	return route.Handler(ctx, client)
}

func (r *Router) handleSubrequest(ctx context.Context, client gwclient.Client, opts map[string]string) (*gwclient.Result, bool, error) {
	switch opts[requestIDKey] {
	case "":
		return nil, false, nil
	case subrequests.RequestSubrequestsDescribe:
		res, err := r.describe()
		return res, true, err
	case bktargets.SubrequestsTargetsDefinition.Name:
		res, err := r.list(ctx, client, opts[keyTarget])
		return res, true, err
	case keyTopLevelTarget:
		return nil, false, nil
	case keyResolveSpec:
		res, err := handleResolveSpec(ctx, client)
		return res, true, err
	case KeyDefaultPlatform:
		res, err := handleDefaultPlatform()
		return res, true, err
	case KeyJSONSchema:
		res, err := handleJSONSchema()
		return res, true, err
	default:
		return nil, false, errors.Errorf("unsupported subrequest %q", opts[requestIDKey])
	}
}

func (r *Router) describe() (*gwclient.Result, error) {
	subs := []subrequests.Request{
		bktargets.SubrequestsTargetsDefinition,
		subrequests.SubrequestsDescribeDefinition,
		{
			Name:        KeyJSONSchema,
			Version:     "1.0.0",
			Type:        "rpc",
			Description: "Returns the JSON schema for Dalec specs",
		},
	}

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

// list returns the target list. For static routes, metadata is available
// directly. For forwarding routes, the remote frontend is queried lazily.
func (r *Router) list(ctx context.Context, client gwclient.Client, target string) (*gwclient.Result, error) {
	spec, err := r.loadSpec(ctx, client)
	if err != nil {
		bklog.G(ctx).WithError(err).Warn("Could not load spec for target list annotation")
		// Continue without spec — targets just won't have SpecDefined set.
	}

	hasSpecTargets := spec != nil && len(spec.Targets) > 0

	var ls TargetList

	keys := maps.Keys(r.routes)
	slices.Sort(keys)

	for _, key := range keys {
		route := r.routes[key]

		// If a filter target is set, only include routes under that prefix.
		if target != "" && key != target && !strings.HasPrefix(key, target+"/") {
			continue
		}

		// Hidden routes are dispatchable but excluded from the target list.
		if route.Info.Hidden {
			continue
		}

		dt := route.Info
		if hasSpecTargets {
			tlk := topLevelKey(key)
			if _, ok := spec.Targets[tlk]; ok {
				dt.SpecDefined = true
			}
		}

		// Forwarding route: query the remote frontend for its sub-targets.
		if route.Forward != nil {
			subTargets, err := queryForwardedTargets(ctx, client, key, route.Forward)
			if err != nil {
				bklog.G(ctx).WithError(err).Warn("Could not query forwarded frontend targets")
				// Fall through to show the single forwarding entry.
				ls.Targets = append(ls.Targets, dt)
				continue
			}
			ls.Targets = append(ls.Targets, subTargets...)
			continue
		}

		ls.Targets = append(ls.Targets, dt)
	}

	return dalecTargetListToResult(ls)
}

// queryForwardedTargets queries a forwarded frontend for its target list
// and prefixes each returned target name with the forwarding key.
func queryForwardedTargets(ctx context.Context, client gwclient.Client, prefix string, fwd *Forward) ([]Target, error) {
	req, err := newSolveRequest(
		copyForForward(ctx, client),
		withSpec(ctx, fwd.Spec, dalec.ProgressGroup("query forwarded target list")),
		toFrontend(fwd.Frontend),
		withTarget(""),
		func(sr *gwclient.SolveRequest) error {
			sr.FrontendOpt[requestIDKey] = bktargets.SubrequestsTargetsDefinition.Name
			return nil
		},
	)
	if err != nil {
		return nil, err
	}

	res, err := client.Solve(ctx, req)
	if err != nil {
		return nil, err
	}

	dt, ok := res.Metadata["result.json"]
	if !ok {
		return nil, errors.New("forwarded frontend did not return result.json")
	}

	var remote bktargets.List
	if err := json.Unmarshal(dt, &remote); err != nil {
		return nil, errors.Wrap(err, "error unmarshalling forwarded target list")
	}

	targets := make([]Target, 0, len(remote.Targets))
	for _, t := range remote.Targets {
		t.Name = path.Join(prefix, t.Name)
		targets = append(targets, Target{Target: t})
	}
	return targets, nil
}

// dalecTargetListToResult serializes a TargetList into a gateway result
// with both JSON and human-readable text representations.
func dalecTargetListToResult(ls TargetList) (*gwclient.Result, error) {
	res := gwclient.NewResult()

	dtJSON, err := json.MarshalIndent(ls, "", "  ")
	if err != nil {
		return nil, errors.Wrap(err, "error marshalling target list to json")
	}
	res.AddMeta("result.json", dtJSON)

	buf := bytes.NewBuffer(nil)
	if err := printTargets(ls, buf); err != nil {
		return nil, err
	}
	res.AddMeta("result.txt", buf.Bytes())

	res.AddMeta("version", []byte(bktargets.SubrequestsTargetsDefinition.Version))
	return res, nil
}

// printTargets writes a human-readable table of targets.
func printTargets(ls TargetList, w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 1, ' ', 0)
	if _, err := fmt.Fprintf(tw, "TARGET\tDESCRIPTION\n"); err != nil {
		return err
	}

	for _, t := range ls.Targets {
		name := t.Name
		if name == "" && t.Default {
			name = "(default)"
		} else if t.Default {
			name = fmt.Sprintf("%s (default)", name)
		}

		if _, err := fmt.Fprintf(tw, "%s\t%s\n", name, t.Description); err != nil {
			return err
		}
	}

	return tw.Flush()
}

// lookupTarget finds the route matching the given target string.
// It tries exact match first, then longest prefix match.
// An empty target is always an error — callers must specify a target key.
func (r *Router) lookupTarget(ctx context.Context, target string) (matchedPath string, _ *Route, _ error) {
	// 1. Exact match
	if route, ok := r.routes[target]; ok {
		return target, &route, nil
	}

	// 2. Empty target — no global default; prompt the user.
	if target == "" {
		return "", nil, handlerNotFound(target, maps.Keys(r.routes))
	}

	// 3. Longest prefix match
	var candidates []string
	for k := range r.routes {
		if strings.HasPrefix(target, k+"/") {
			candidates = append(candidates, k)
		}
	}

	if len(candidates) > 0 {
		slices.Sort(candidates)
		k := candidates[len(candidates)-1]
		route := r.routes[k]
		bklog.G(ctx).WithField("prefix", k).WithField("target", target).Info("Using prefix match for target")
		return k, &route, nil
	}

	return "", nil, handlerNotFound(target, maps.Keys(r.routes))
}

func (r *Router) loadSpec(ctx context.Context, client gwclient.Client) (*dalec.Spec, error) {
	if r.spec != nil {
		return r.spec, nil
	}
	dc, err := dockerui.NewClient(client)
	if err != nil {
		return nil, err
	}

	spec, err := LoadSpec(ctx, dc, nil, func(cfg *LoadConfig) {
		cfg.SubstituteOpts = append(cfg.SubstituteOpts, dalec.WithAllowAnyArg)
	})
	if err != nil {
		return nil, err
	}
	r.spec = spec
	return spec, nil
}

// topLevelKey returns the first path segment of a route path.
// e.g. "azlinux3/container/depsonly" → "azlinux3"
func topLevelKey(routePath string) string {
	if i := strings.IndexByte(routePath, '/'); i >= 0 {
		return routePath[:i]
	}
	return routePath
}

// WithTargetForwardingHandler registers a forwarding handler for each
// spec target that has a custom frontend. This replaces any builtin
// routes for that target key prefix.
func WithTargetForwardingHandler(ctx context.Context, client gwclient.Client, r *Router) error {
	if k := GetTargetKey(client); k != "" {
		return fmt.Errorf("target forwarding requested but target is already forwarded: this is a bug in the frontend for %q", k)
	}
	spec, err := r.loadSpec(ctx, client)
	if err != nil {
		return err
	}

	for key, t := range spec.Targets {
		if t.Frontend == nil {
			continue
		}

		// Capture loop variables for the closure.
		frontendKey := key
		frontendTarget := t

		// Remove any existing builtin routes under this key prefix
		// and replace with a single forwarding route.
		for routePath := range r.routes {
			if routePath == frontendKey || strings.HasPrefix(routePath, frontendKey+"/") {
				delete(r.routes, routePath)
			}
		}

		r.Add(ctx, Route{
			FullPath: frontendKey,
			Handler: func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
				ctx = bklog.WithLogger(ctx, bklog.G(ctx).
					WithField("frontend", frontendKey).
					WithField("frontend-ref", frontendTarget.Frontend.Image).
					WithField("forwarded", true))
				bklog.G(ctx).Info("Forwarding to custom frontend")

				// Strip the forwarding key prefix so the remote frontend
				// sees only the sub-target (e.g. "phony/check" → "check").
				fwdTarget := strings.TrimPrefix(client.BuildOpts().Opts[keyTarget], frontendKey)
				fwdTarget = strings.TrimPrefix(fwdTarget, "/")

				req, err := newSolveRequest(
					copyForForward(ctx, client),
					withSpec(ctx, spec, dalec.ProgressGroup("prepare spec to forward to frontend")),
					toFrontend(frontendTarget.Frontend),
					withTarget(fwdTarget),
				)
				if err != nil {
					return nil, err
				}

				return client.Solve(ctx, req)
			},
			Info: Target{
				Target: bktargets.Target{
					Name:        frontendKey,
					Description: fmt.Sprintf("Forwarded to custom frontend: %s", frontendTarget.Frontend.Image),
				},
			},
			Forward: &Forward{
				Spec:     spec,
				Frontend: frontendTarget.Frontend,
			},
		})

		bklog.G(ctx).
			WithField("target", frontendKey).
			WithField("routes", maps.Keys(r.routes)).
			Info("Added custom frontend forwarding route")
	}

	return nil
}
