package testenv

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/solver/pb"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
)

var InlineBuildOutput bool

func buildBaseFrontend(ctx context.Context, c gwclient.Client) (*gwclient.Result, error) {
	dc, err := dockerui.NewClient(c)
	if err != nil {
		return nil, errors.Wrap(err, "error creating dockerui client")
	}

	buildCtx, err := dc.MainContext(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "error getting main context")
	}

	def, err := buildCtx.Marshal(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "error marshaling main context")
	}

	// Can't use the state from `MainContext` because it filters out
	// whatever was in `.dockerignore`, which may include `Dockerfile`,
	// which we need.
	dockerfileDef, err := llb.Local(dockerui.DefaultLocalNameDockerfile, llb.IncludePatterns([]string{"Dockerfile"})).Marshal(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "error marshaling Dockerfile context")
	}

	defPB := def.ToPB()
	return c.Solve(ctx, gwclient.SolveRequest{
		Frontend:    "dockerfile.v0",
		FrontendOpt: map[string]string{},
		FrontendInputs: map[string]*pb.Definition{
			dockerui.DefaultLocalNameContext:    defPB,
			dockerui.DefaultLocalNameDockerfile: dockerfileDef.ToPB(),
		},
		Evaluate: true,
	})
}

// InjectInput adds the necessary options to a solve request to use the output of the provided build function as an input to the solve request.
func injectInput(ctx context.Context, res *gwclient.Result, id string, req *gwclient.SolveRequest) (retErr error) {
	ctx, span := otel.Tracer("").Start(ctx, "build input "+id)
	defer func() {
		if retErr != nil {
			span.SetStatus(codes.Error, retErr.Error())
		}
		span.End()
	}()

	ref, err := res.SingleRef()
	if err != nil {
		return err
	}

	st, err := ref.ToState()
	if err != nil {
		return err
	}

	dt := res.Metadata[exptypes.ExporterImageConfigKey]

	if dt != nil {
		st, err = st.WithImageConfig(dt)
		if err != nil {
			return err
		}
	}

	def, err := st.Marshal(ctx)
	if err != nil {
		return err
	}

	if req.FrontendOpt == nil {
		req.FrontendOpt = make(map[string]string)
	}
	req.FrontendOpt["context:"+id] = "input:" + id
	if req.FrontendInputs == nil {
		req.FrontendInputs = make(map[string]*pb.Definition)
	}
	req.FrontendInputs[id] = def.ToPB()
	if dt != nil {
		meta := map[string][]byte{
			exptypes.ExporterImageConfigKey: dt,
		}
		metaDt, err := json.Marshal(meta)
		if err != nil {
			return errors.Wrap(err, "error marshaling local frontend metadata")
		}
		req.FrontendOpt["input-metadata:"+id] = string(metaDt)
	}

	return nil
}

// withDalecInput adds the necessary options to a solve request to use
// the locally built frontend as an input to the solve request.
// This only works with buildkit >= 0.12
func withDalecInput(ctx context.Context, gwc gwclient.Client, opts *gwclient.SolveRequest) error {
	id := identity.NewID()
	res, err := buildBaseFrontend(ctx, gwc)
	if err != nil {
		return errors.Wrap(err, "error building local frontend")
	}
	if err := injectInput(ctx, res, id, opts); err != nil {
		return errors.Wrap(err, "error adding local frontend as input")
	}

	opts.FrontendOpt["source"] = id
	opts.Frontend = "gateway.v0"
	return nil
}

type testWriter struct {
	t   *testing.T
	buf *bytes.Buffer
}

func (tw *testWriter) Write(p []byte) (n int, err error) {
	n, err = tw.buf.Write(p)
	if err != nil {
		return n, err
	}

	// Flush complete lines only
	for {
		dt := tw.buf.Bytes()
		idx := bytes.IndexRune(dt, '\n')
		if idx < 0 {
			break
		}
		tw.t.Log(string(dt[:idx]))
		tw.buf.Next(idx + 1)
	}

	return n, nil
}

func (tw *testWriter) Flush() {
	if tw.buf.Len() == 0 {
		return
	}
	scanner := bufio.NewScanner(tw.buf)
	for scanner.Scan() {
		tw.t.Log(scanner.Text())
	}
	tw.buf.Reset()
}

var logBufferPool = &sync.Pool{New: func() any {
	return bytes.NewBuffer(nil)
}}

func outputStreamStatusFn(ctx context.Context, t *testing.T) (func(*client.SolveStatus), func()) {
	var (
		warnings []*client.VertexWarning
		errOnce  sync.Once

		done = make(chan struct{})
		w    = getBuildOutputStream(ctx, t, done)
	)

	fn := func(msg *client.SolveStatus) {
		warnings = append(warnings, msg.Warnings...)
		for _, l := range msg.Logs {
			if _, err := w.Write(l.Data); err != nil {
				errOnce.Do(func() {
					// Don't spam the logs with multiple errors
					t.Logf("error writing log data: %v", err)
				})
			}
		}
	}

	return fn, func() {
		for _, v := range warnings {
			t.Logf("WARNING: %s", string(v.Short))
		}
		close(done)
	}
}

func multiStatusFunc(fns ...func(*client.SolveStatus)) func(*client.SolveStatus) {
	return func(msg *client.SolveStatus) {
		for _, fn := range fns {
			if fn == nil {
				continue
			}
			fn(msg)
		}
	}
}

func getBuildOutputStream(ctx context.Context, t *testing.T, done <-chan struct{}) io.Writer {
	t.Helper()

	if InlineBuildOutput {
		buf := logBufferPool.Get().(*bytes.Buffer)
		tw := &testWriter{t: t, buf: buf}
		t.Cleanup(func() {
			select {
			case <-ctx.Done():
				return
			case <-done:
			}
			tw.Flush()
			logBufferPool.Put(buf)
		})
		return tw
	}

	dir := t.TempDir()
	f, err := os.OpenFile(filepath.Join(dir, "build.log"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatalf("error opening temp file: %v", err)
	}
	t.Cleanup(func() {
		defer f.Close()

		select {
		case <-ctx.Done():
			return
		case <-done:
		}

		_, err = f.Seek(0, io.SeekStart)
		if err != nil {
			t.Log(err)
			return
		}

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			t.Log(scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			t.Log(err)
		}
	})

	return f
}

func fowardToSolveStatusFn(ctx context.Context, ch <-chan *client.SolveStatus, fns ...func(*client.SolveStatus)) {
	f := multiStatusFunc(fns...)
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if msg != nil {
				f(msg)
				continue
			}
			if !ok {
				return
			}
		}
	}
}

// withProjectRoot adds the current project root as the build context for the solve request.
func withProjectRoot(opts *client.SolveOpt) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	projectRoot, err := lookupProjectRoot(cwd)
	if err != nil {
		return err
	}

	if opts.LocalDirs == nil {
		opts.LocalDirs = make(map[string]string)
	}
	opts.LocalDirs[dockerui.DefaultLocalNameContext] = projectRoot
	opts.LocalDirs[dockerui.DefaultLocalNameDockerfile] = projectRoot
	return nil
}

// lookupProjectRoot looks up the project root from the current working directory.
// This is needed so the test suite can be run from any directory within the project.
func lookupProjectRoot(cur string) (string, error) {
	if _, err := os.Stat(filepath.Join(cur, "go.mod")); err != nil {
		if cur == "/" || cur == "." {
			return "", errors.Wrap(err, "could not find project root")
		}
		if os.IsNotExist(err) {
			return lookupProjectRoot(filepath.Dir(cur))
		}
		return "", err
	}

	return cur, nil
}
