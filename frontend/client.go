package frontend

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/containerd/platforms"
	"github.com/goccy/go-yaml"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/internal/gwutil"
)

const (
	keyResolveSpec = "frontend.dalec.resolve"

	// KeyDefaultPlatform is the subrequest id for returning the default platform
	// for the builder.
	KeyDefaultPlatform = "frontend.dalec.defaultPlatform"
	KeyJSONSchema      = "frontend.dalec.schema"
)

const keyTarget = "target"

// noSuchHandlerError is returned when no route matches the requested target.
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

var (
	_ gwclient.Client = (*clientWithCustomOpts)(nil)
)

type clientWithCustomOpts struct {
	opts gwclient.BuildOpts
	gwclient.Client
}

func (d *clientWithCustomOpts) BuildOpts() gwclient.BuildOpts {
	return d.opts
}

func setClientOptOption(client gwclient.Client, extraOpts map[string]string) gwclient.Client {
	opts := client.BuildOpts()

	for key, value := range extraOpts {
		opts.Opts[key] = value
	}
	return gwutil.WithCurrentFrontend(client, &clientWithCustomOpts{
		Client: client,
		opts:   opts,
	})
}

func maybeSetDalecTargetKey(client gwclient.Client, key string) gwclient.Client {
	opts := client.BuildOpts()
	if opts.Opts[keyTopLevelTarget] != "" {
		// do nothing since this is already set
		return client
	}

	// Optimization to help prevent unnecessary grpc requests.
	// The gateway client will make a grpc request to get the build opts from the gateway.
	// This just caches those opts locally.
	// If the client is already a clientWithCustomOpts, then the opts are already cached.
	if _, ok := client.(*clientWithCustomOpts); !ok {
		// this forces the client to use our cached opts from above
		client = gwutil.WithCurrentFrontend(client, &clientWithCustomOpts{opts: opts, Client: client})
	}
	return setClientOptOption(client, map[string]string{keyTopLevelTarget: key, "build-arg:" + dalec.KeyDalecTarget: key})
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

func handleJSONSchema() (*gwclient.Result, error) {
	res := gwclient.NewResult()
	jsonSchema := dalec.GetJSONSchema()
	res.AddMeta("result.json", jsonSchema)
	res.AddMeta("result.txt", jsonSchema)

	return res, nil
}
