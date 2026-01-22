package test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/containerd/platforms"
	"github.com/goccy/go-yaml"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests/targets"
	"github.com/moby/buildkit/solver/pb"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
	"github.com/tonistiigi/fsutil/types"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

const (
	phonyRef       = "dalec/integration/frontend/phony"
	phonySignerRef = "dalec/integration/signer/phony"
)

func startTestSpan(ctx context.Context, t *testing.T) context.Context {
	ctx, span := otel.Tracer("").Start(ctx, t.Name())
	t.Cleanup(func() {
		if t.Failed() {
			span.SetStatus(codes.Error, "test failed")
		}
		span.End()
	})
	return ctx
}

// specToSolveRequest injects the spec as the build context into the solve request.
func specToSolveRequest(ctx context.Context, t *testing.T, spec *dalec.Spec, sr *gwclient.SolveRequest) {
	t.Helper()

	dt, err := yaml.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}

	def, err := llb.Scratch().File(llb.Mkfile("Dockerfile", 0o644, dt)).Marshal(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if sr.FrontendInputs == nil {
		sr.FrontendInputs = make(map[string]*pb.Definition)
	}

	sr.FrontendInputs[dockerui.DefaultLocalNameContext] = def.ToPB()
	sr.FrontendInputs[dockerui.DefaultLocalNameDockerfile] = def.ToPB()
}

func readFile(ctx context.Context, t *testing.T, name string, res *gwclient.Result) []byte {
	t.Helper()

	ref, err := res.SingleRef()
	if err != nil {
		t.Fatal(err)
	}

	dt, err := ref.ReadFile(ctx, gwclient.ReadRequest{
		Filename: name,
	})
	if err != nil {
		dir := filepath.Dir(name)
		if strings.Contains(err.Error(), "not a directory") {
			dir = filepath.Dir(dir)
		}
		stat, _ := ref.ReadDir(ctx, gwclient.ReadDirRequest{
			Path: filepath.Dir(dir),
		})
		t.Fatalf("error reading file %q: %v, dir contents of %s: \n%s", name, err, dir, dirStatAsStringer(stat))
	}

	return dt
}

func maybeReadFile(ctx context.Context, name string, res *gwclient.Result) ([]byte, error) {
	ref, err := res.SingleRef()
	if err != nil {
		return nil, err
	}

	dt, err := ref.ReadFile(ctx, gwclient.ReadRequest{
		Filename: name,
	})
	if err != nil {
		stat, _ := ref.ReadDir(ctx, gwclient.ReadDirRequest{
			Path: filepath.Dir(name),
		})
		return nil, fmt.Errorf("error reading file %q: %v, dir contents: \n%s", name, err, dirStatAsStringer(stat))
	}

	return dt, nil
}

func statFile(ctx context.Context, t *testing.T, name string, res *gwclient.Result) {
	t.Helper()

	ref, err := res.SingleRef()
	if err != nil {
		t.Fatal(err)
	}

	_, err = ref.StatFile(ctx, gwclient.StatRequest{
		Path: name,
	})
	if err != nil {
		t.Fatalf("expected spec.yml to exist in debug/resolve target, got error: %v", err)
	}
}

func checkFile(ctx context.Context, t *testing.T, name string, res *gwclient.Result, expect []byte) {
	t.Helper()

	dt := readFile(ctx, t, name, res)
	assert.Check(t, cmp.Equal(string(dt), string(expect)))
}

func listTargets(ctx context.Context, t *testing.T, gwc gwclient.Client, spec *dalec.Spec) targets.List {
	t.Helper()

	sr := newSolveRequest(withListTargetsOnly, withSpec(ctx, t, spec))
	res := solveT(ctx, t, gwc, sr)

	dt, ok := res.Metadata["result.json"]
	if !ok {
		t.Fatal("missing result.json from list targets")
	}

	var ls targets.List
	if err := json.Unmarshal(dt, &ls); err != nil {
		t.Fatalf("could not unmsarshal list targets result: %v", err)
	}
	return ls
}

func containsTarget(ls targets.List, name string) bool {
	return slices.ContainsFunc(ls.Targets, func(tgt targets.Target) bool {
		return tgt.Name == name
	})
}

func checkTargetExists(t *testing.T, ls targets.List, name string) {
	t.Helper()

	if !containsTarget(ls, name) {
		names := make([]string, 0, len(ls.Targets))
		for _, tgt := range ls.Targets {
			names = append(names, tgt.Name)
		}

		t.Fatalf("did not find target %q:\n%v", name, names)
	}
}

type dirStatAsStringer []*types.Stat

func (d dirStatAsStringer) String() string {
	var buf bytes.Buffer
	buf.WriteString("\n")
	for _, s := range d {
		fmt.Fprintf(&buf, "%s %s %d %d\n", s.GetPath(), fs.FileMode(s.Mode), s.Uid, s.Gid)
	}
	return buf.String()
}

type newSolveRequestConfig struct {
	req              *gwclient.SolveRequest
	noFillSpecFields bool
}

// srOpt is used by [newSolveRequest] to apply changes to a [gwclient.SolveRequest].
type srOpt func(*newSolveRequestConfig)

func newSolveRequest(opts ...srOpt) gwclient.SolveRequest {
	cfg := newSolveRequestConfig{
		req: &gwclient.SolveRequest{Evaluate: true},
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return *cfg.req
}

func withPlatform(platform ocispecs.Platform) srOpt {
	return func(cfg *newSolveRequestConfig) {
		if cfg.req.FrontendOpt == nil {
			cfg.req.FrontendOpt = make(map[string]string)
		}
		cfg.req.FrontendOpt["platform"] = platforms.Format(platform)
	}
}

func withPlatformPtr(p *ocispecs.Platform) srOpt {
	return func(cfg *newSolveRequestConfig) {
		if p == nil {
			return
		}
		withPlatform(*p)(cfg)
	}
}

func withBuildArg(k, v string) srOpt {
	return func(cfg *newSolveRequestConfig) {
		cfg.req.FrontendOpt["build-arg:"+k] = v
	}
}

func withSpec(ctx context.Context, t *testing.T, spec *dalec.Spec) srOpt {
	return func(cfg *newSolveRequestConfig) {
		if spec != nil && !cfg.noFillSpecFields {
			if spec.Packager == "" {
				spec.Packager = "test"
			}
			if spec.Website == "" {
				spec.Website = "https://github.com/project-dalec/dalec"
			}
		}
		specToSolveRequest(ctx, t, spec, cfg.req)
	}
}

func withBuildTarget(target string) srOpt {
	return func(cfg *newSolveRequestConfig) {
		if cfg.req.FrontendOpt == nil {
			cfg.req.FrontendOpt = make(map[string]string)
		}
		cfg.req.FrontendOpt["target"] = target
	}
}

func withIgnoreCache(refs ...string) srOpt {
	return func(cfg *newSolveRequestConfig) {
		if cfg.req.FrontendOpt == nil {
			cfg.req.FrontendOpt = make(map[string]string)
		}
		v := cfg.req.FrontendOpt["no-cache"]
		if v != "" {
			v += ","
		}
		cfg.req.FrontendOpt["no-cache"] = v + strings.Join(refs, ",")

	}
}

func withSubrequest(id string) srOpt {
	return func(cfg *newSolveRequestConfig) {
		if cfg.req.FrontendOpt == nil {
			cfg.req.FrontendOpt = make(map[string]string)
		}
		cfg.req.FrontendOpt["requestid"] = id
	}
}

// withListTargetsOnly sets up the request so that we do a subrequest to just list targets
// None of the targets will be run with this set.
func withListTargetsOnly(cfg *newSolveRequestConfig) {
	withSubrequest(targets.RequestTargets)(cfg)
}

func solveT(ctx context.Context, t *testing.T, gwc gwclient.Client, req gwclient.SolveRequest) *gwclient.Result {
	t.Helper()
	res, err := gwc.Solve(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func solveTCh(ctx context.Context, t *testing.T, gwc gwclient.Client, req gwclient.SolveRequest, rc chan<- *gwclient.Result, ec chan<- error) {
	t.Helper()

	go func() {
		t.Helper()
		res, err := gwc.Solve(ctx, req)
		if err != nil {
			ec <- err
		}

		rc <- res
	}()
}

func withMainContext(ctx context.Context, t *testing.T, st llb.State) srOpt {
	return func(cfg *newSolveRequestConfig) {
		if cfg.req.FrontendOpt == nil {
			cfg.req.FrontendOpt = make(map[string]string)
		}
		if cfg.req.FrontendInputs == nil {
			cfg.req.FrontendInputs = make(map[string]*pb.Definition)
		}

		def, err := st.Marshal(ctx)
		if err != nil {
			t.Fatal(err)
		}

		cfg.req.FrontendInputs[dockerui.DefaultLocalNameContext] = def.ToPB()
	}
}

func withBuildContext(ctx context.Context, t *testing.T, name string, st llb.State) srOpt {
	return func(cfg *newSolveRequestConfig) {
		if cfg.req.FrontendOpt == nil {
			cfg.req.FrontendOpt = make(map[string]string)
		}
		if cfg.req.FrontendInputs == nil {
			cfg.req.FrontendInputs = make(map[string]*pb.Definition)
		}

		def, err := st.Marshal(ctx)
		if err != nil {
			t.Fatal(err)
		}

		cfg.req.FrontendOpt["context:"+name] = "input:" + name
		cfg.req.FrontendInputs[name] = def.ToPB()
	}
}

func reqToState(ctx context.Context, gwc gwclient.Client, sr gwclient.SolveRequest, t *testing.T) llb.State {
	t.Helper()
	res := solveT(ctx, t, gwc, sr)

	ref, err := res.SingleRef()
	if err != nil {
		t.Fatal(err)
	}

	st, err := ref.ToState()
	if err != nil {
		t.Fatal(err)
	}

	dt, ok := res.Metadata[exptypes.ExporterPlatformsKey]
	if ok {
		var pls exptypes.Platforms
		if err := json.Unmarshal(dt, &pls); err != nil {
			t.Fatal(err)
		}
		st = st.Platform(pls.Platforms[0].Platform)
	}

	return st
}

func readDefaultPlatform(ctx context.Context, t *testing.T, gwc gwclient.Client) ocispecs.Platform {
	req := newSolveRequest(withSubrequest(frontend.KeyDefaultPlatform), withSpec(ctx, t, nil))
	res := solveT(ctx, t, gwc, req)

	dt := res.Metadata["result.json"]
	var p ocispecs.Platform

	err := json.Unmarshal(dt, &p)
	assert.NilError(t, err)
	return p
}

func validatePathAndPermissions(ctx context.Context, ref gwclient.Reference, path string, expected os.FileMode) error {
	stat, err := ref.StatFile(ctx, gwclient.StatRequest{Path: path})
	if err != nil {
		return err
	}

	got := os.FileMode(stat.Mode).Perm()

	if expected != got {
		return fmt.Errorf("expected permissions %o to equal expected %o", got, expected)
	}
	return nil
}
