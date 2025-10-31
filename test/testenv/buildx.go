package testenv

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cpuguy83/dockercfg"
	"github.com/moby/buildkit/client"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session/auth"
	"github.com/moby/buildkit/session/secrets/secretsprovider"
	"github.com/moby/buildkit/session/sshforward/sshprovider"
	"github.com/moby/buildkit/solver/pb"
	spb "github.com/moby/buildkit/sourcepolicy/pb"
	"github.com/opencontainers/go-digest"
	pkgerrors "github.com/pkg/errors"
	"github.com/project-dalec/dalec/sessionutil/socketprovider"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"gotest.tools/v3/assert"
)

type BuildxEnv struct {
	builder string

	mu     sync.Mutex
	client *client.Client

	supportedOnce sync.Once
	supportedErr  error

	refs map[string]gwclient.BuildFunc
}

func New() *BuildxEnv {
	return &BuildxEnv{}
}

func (b *BuildxEnv) WithBuilder(builder string) *BuildxEnv {
	b.builder = builder
	return b
}

// Load loads the output of the specified [gwclient.BuildFunc] into the buildkit instance.
func (b *BuildxEnv) Load(ctx context.Context, id string, f gwclient.BuildFunc) error {
	if b.refs == nil {
		b.refs = make(map[string]gwclient.BuildFunc)
	}
	b.refs[id] = f
	return nil
}

func (b *BuildxEnv) supportsDialStdio(ctx context.Context) (bool, error) {
	// Check `docker buildx --help` output to see if `dial-stdio` is listed.
	// If its listed then dial-stdio is supported.
	cmd := exec.CommandContext(ctx, "docker", "buildx", "--help")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, pkgerrors.Wrap(err, string(out))
	}
	return strings.Contains(string(out), "dial-stdio"), nil
}

var errDialStdioNotSupported = errors.New("buildx dial-stdio not supported")

type connCloseWrapper struct {
	net.Conn
	close func()
}

func (c *connCloseWrapper) Close() error {
	if c.close != nil {
		c.close()
	}
	if err := c.Conn.Close(); err != nil {
		return err
	}
	return nil
}

func (b *BuildxEnv) dialStdio(ctx context.Context) error {
	c, err := client.New(ctx, "", client.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		args := []string{"buildx", "dial-stdio", "--progress=plain"}
		if b.builder != "" {
			args = append(args, "--builder="+b.builder)
		}

		// NOTE: Do *not* use exec.CommandContext here as it will prevent proper cleanup of the process
		// or more specifically, the subprocess it spawns.
		// This is because go sends SIGKILL forcing the process to exit immediately, which prevents
		// the buildx dial-stdio process from cleaning up its resources properly.
		cmd := exec.Command("docker", args...)
		cmd.Env = os.Environ()

		c1, c2 := net.Pipe()
		cmd.Stdin = c1
		cmd.Stdout = c1

		// Use a pipe to check when the connection is actually complete
		// Also write all of stderr to an error buffer so we can have more details
		// in the error message when the command fails.
		r, w := io.Pipe()
		errBuf := bytes.NewBuffer(nil)
		ww := io.MultiWriter(w, errBuf)
		cmd.Stderr = ww

		if err := cmd.Start(); err != nil {
			return nil, err
		}

		chWait := make(chan struct{})
		go func() {
			err := cmd.Wait()
			c1.Close()
			// pkgerrors.Wrap will return nil if err is nil, otherwise it will give
			// us a wrapped error with the buffered stderr from he command.
			w.CloseWithError(pkgerrors.Wrapf(err, "%s", errBuf))
		}()

		defer r.Close()

		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			txt := strings.ToLower(scanner.Text())

			if strings.HasPrefix(txt, "#1 dialing builder") && strings.HasSuffix(txt, "done") {
				go func() {
					// Continue draining stderr so the process does not get blocked
					_, _ = io.Copy(io.Discard, r)
				}()
				break
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, err
		}

		out := &connCloseWrapper{
			Conn: c2,
			close: sync.OnceFunc(func() {
				// Send 2 interrupt signals to the process to ensure it exits gracefully
				// This is how buildx/docker plugins handle termination

				cmd.Process.Signal(os.Interrupt) //nolint:errcheck // We don't care about this error, we are going to send another one anyway
				if err := cmd.Process.Signal(os.Interrupt); err != nil {
					cmd.Process.Kill() //nolint:errcheck //  Force kill if interrupt fails
				}

				select {
				case <-chWait:
				case <-time.After(10 * time.Second):
					// If it still doesn't exit, force kill
					cmd.Process.Kill() //nolint:errcheck // Force kill if it doesn't exit after interrupt
				}
			}),
		}

		return out, nil
	}))

	if err != nil {
		return err
	}

	b.client = c
	return nil
}

// bootstrap is ultimately responsible for creating a buildkit client.
func (b *BuildxEnv) bootstrap(ctx context.Context) (retErr error) {
	if b.client != nil {
		return nil
	}

	defer func() {
		if retErr != nil {
			return
		}

		b.supportedOnce.Do(func() {
			info, err := b.client.Info(ctx)
			if err != nil {
				b.supportedErr = pkgerrors.WithStack(err)
				return
			}

			if !supportsFrontendAsInput(info) {
				b.supportedErr = fmt.Errorf("buildkit version not supported: min version is v%s, got: %s", minVersion, info.BuildkitVersion.Version)
			}
		})
		if b.supportedErr != nil {
			b.client.Close() //nolint:errcheck
			b.client = nil
			retErr = b.supportedErr
		}
	}()

	ok, err := b.supportsDialStdio(ctx)
	if err != nil {
		return fmt.Errorf("%w: %w", errDialStdioNotSupported, err)
	}

	if !ok {
		return errDialStdioNotSupported
	}

	return b.dialStdio(ctx)
}

func (b *BuildxEnv) Buildkit(ctx context.Context) (*client.Client, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if err := b.bootstrap(ctx); err != nil {
		return nil, err
	}

	if b.client != nil {
		return b.client, nil
	}

	panic("unreachable: if you see this then this is a bug in the testenv bootstrap code")
}

type FrontendSpec struct {
	ID    string
	Build gwclient.BuildFunc
}

// withResolveLocal tells buildkit to prefer local images when resolving image references.
// This prevents unnecessary API requests to registries.
func withResolveLocal(so *client.SolveOpt) {
	if so.FrontendAttrs == nil {
		so.FrontendAttrs = make(map[string]string)
	}

	if _, ok := so.FrontendAttrs[pb.AttrImageResolveMode]; ok {
		// Don't set it if it's already set.
		return
	}

	so.FrontendAttrs[pb.AttrImageResolveMode] = pb.AttrImageResolveModePreferLocal
}

type TestFunc func(context.Context, gwclient.Client)

type TestRunnerConfig struct {
	// SolveStatusFn replaces the builtin status logger with a custom implementation.
	// This is useful particularly if you need to inspect the solve statuses.
	SolveStatusFn func(*client.SolveStatus)
	SolveOptFns   []func(*client.SolveOpt)
	// SocketProxies is the list of sockets that need to be forwarded into the build.
	SocketProxies []socketprovider.ProxyConfig
}

type TestRunnerOpt func(*TestRunnerConfig)

// SolveStatus is convenience wrapper for [client.SolveStatus] to help disambiguate
// imports of the [client] package.
type SolveStatus = client.SolveStatus
type SolveOpt = client.SolveOpt

func WithSolveStatusFn(f func(*SolveStatus)) TestRunnerOpt {
	return func(cfg *TestRunnerConfig) {
		cfg.SolveStatusFn = f
	}
}

func WithSecrets(k, v string) TestRunnerOpt {
	return func(cfg *TestRunnerConfig) {
		cfg.SolveOptFns = append(cfg.SolveOptFns, func(so *client.SolveOpt) {
			m := map[string][]byte{
				k: []byte(v),
			}
			so.Session = append(so.Session, secretsprovider.FromMap(m))
		})
	}
}

func WithSSHSocket(id, addr string) TestRunnerOpt {
	return func(cfg *TestRunnerConfig) {
		a, err := sshprovider.NewSSHAgentProvider([]sshprovider.AgentConfig{
			{
				ID:    id,
				Paths: []string{addr},
			},
		})
		if err != nil {
			panic(err)
		}
		cfg.SolveOptFns = append(cfg.SolveOptFns, func(so *client.SolveOpt) {
			so.Session = append(so.Session, a)
		})
	}
}

func WithHostNetworking(trc *TestRunnerConfig) {
	trc.SolveOptFns = append(trc.SolveOptFns, func(so *client.SolveOpt) {
		so.AllowedEntitlements = append(so.AllowedEntitlements, "network.host")
	})
}

// This function just puts the opts before the function argument, which makes
// it harder to miss what's happening with the opts for those unfamiliar with
// the pattern.
func (b *BuildxEnv) RunTestOptsFirst(ctx context.Context, t *testing.T, opts []TestRunnerOpt, f TestFunc) {
	b.RunTest(ctx, t, f, opts...)
}

func WithSocketProxies(proxies ...socketprovider.ProxyConfig) TestRunnerOpt {
	return func(cfg *TestRunnerConfig) {
		cfg.SocketProxies = append(cfg.SocketProxies, proxies...)
	}
}

func (b *BuildxEnv) RunTest(ctx context.Context, t *testing.T, f TestFunc, opts ...TestRunnerOpt) {
	var cfg TestRunnerConfig

	for _, o := range opts {
		o(&cfg)
	}

	c, err := b.Buildkit(ctx)
	if err != nil {
		t.Fatalf("%+v", err)
	}

	var (
		ch chan *client.SolveStatus
	)

	if cfg.SolveStatusFn != nil {
		ch = make(chan *client.SolveStatus, 1)
		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				select {
				case <-ctx.Done():
					return
				case msg, ok := <-ch:
					cfg.SolveStatusFn(msg)
					if !ok {
						return
					}
				}
			}
		}()
		t.Cleanup(func() {
			// Make sure message processing is done before the test ends
			select {
			case <-ctx.Done():
			case <-done:
			}
		})
	} else {
		ch = displaySolveStatus(ctx, t)
	}

	var so client.SolveOpt
	withProjectRoot(t, &so)
	withResolveLocal(&so)
	withSocketProxies(t, cfg.SocketProxies)(&so)
	withDockerAuth(&so)

	err = withSourcePolicy(&so)
	assert.NilError(t, err)

	for _, f := range cfg.SolveOptFns {
		f(&so)
	}

	_, err = c.Build(ctx, so, "", func(ctx context.Context, gwc gwclient.Client) (*gwclient.Result, error) {
		gwc = &clientForceDalecWithInput{gwc}

		b.mu.Lock()
		for id, f := range b.refs {
			gwc = wrapWithInput(gwc, id, f)
		}
		b.mu.Unlock()
		f(ctx, gwc)
		return gwclient.NewResult(), nil
	}, ch)

	if err != nil {
		t.Fatal(err)
	}
}

var (
	netHostTestEnv            *BuildxEnv
	netHostTestEnvOnce        sync.Once
	netHostTestEnvCleanupOnce sync.Once
)

// `NewWithNetHostBuildxInstance` creates a buildx instance with host networking enabled.
func NewWithNetHostBuildxInstance(ctx context.Context, t *testing.T) *BuildxEnv {
	dgst := digest.Canonical.FromString(t.Name()).Encoded()
	name := "dalec_integration_test_" + dgst[:12]

	netHostTestEnvOnce.Do(func() {
		netHostTestEnv = New().WithBuilder(name)
		ctxT, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := netHostTestEnv.bootstrap(ctxT); err != nil {
			cmd := exec.CommandContext(ctx, "docker", "buildx", "create", "--name", name, "--driver", "docker-container", "--driver-opt", "network=host", "--buildkitd-flags", "'--allow-insecure-entitlement=network.host'")
			out, err := cmd.CombinedOutput()
			assert.NilError(t, err, "failed to create buildx builder: %s", string(out))

			t.Cleanup(func() {
				netHostTestEnvCleanupOnce.Do(func() {
					ctx := context.WithoutCancel(ctx)
					cmd := exec.CommandContext(ctx, "docker", "buildx", "rm", name)
					out, err := cmd.CombinedOutput()
					assert.NilError(t, err, "failed to remove buildx builder: %s", string(out))
				})
			})

			cmd = exec.CommandContext(ctx, "docker", "buildx", "inspect", name, "--bootstrap")
			out, err = cmd.CombinedOutput()
			assert.NilError(t, err, "failed to create buildx builder: %s", string(out))

			netHostTestEnv = New().WithBuilder(name)
			if err := netHostTestEnv.bootstrap(ctx); err != nil {
				t.Fatalf("failed to bootstrap buildx environment: %v", err)
			}
		}

	})

	return netHostTestEnv
}

// clientForceDalecWithInput is a gwclient.Client that forces the solve request to use the main dalec frontend.
type clientForceDalecWithInput struct {
	gwclient.Client
}

func (c *clientForceDalecWithInput) Solve(ctx context.Context, req gwclient.SolveRequest) (*gwclient.Result, error) {
	if req.Definition == nil {
		// Only inject the frontend when there is no "definition" set.
		// If a definition is set, it is intended for this to go directly to the buildkit solver.
		if err := withDalecInput(ctx, c.Client, &req); err != nil {
			return nil, err
		}
	}
	return c.Client.Solve(ctx, req)
}

// gwClientInputInject is a gwclient.Client that injects the result of a build func into the solve request as an input named by the id.
// This is used to inject a custom frontend into the solve request.
// This does not change what frontend is used, but it does add the custom frontend as an input to the solve request.
// This is so we don't need to have an actual external image from a registry or docker image store.
type gwClientInputInject struct {
	gwclient.Client

	id string
	f  gwclient.BuildFunc
}

func wrapWithInput(c gwclient.Client, id string, f gwclient.BuildFunc) *gwClientInputInject {
	return &gwClientInputInject{
		Client: c,
		id:     id,
		f:      f,
	}
}

func (c *gwClientInputInject) Solve(ctx context.Context, req gwclient.SolveRequest) (*gwclient.Result, error) {
	res, err := c.f(ctx, c.Client)
	if err != nil {
		return nil, err
	}
	if err := injectInput(ctx, res, c.id, &req); err != nil {
		return nil, err
	}
	return c.Client.Solve(ctx, req)
}

func withSourcePolicy(so *client.SolveOpt) error {
	p := os.Getenv("EXPERIMENTAL_BUILDKIT_SOURCE_POLICY")
	if p == "" {
		return nil
	}

	dt, err := os.ReadFile(p)
	if err != nil {
		return fmt.Errorf("could not read source policy file: %w", err)
	}

	var pol spb.Policy
	if err := json.Unmarshal(dt, &pol); err != nil {
		// maybe it's in protobuf format?
		e2 := proto.Unmarshal(dt, &pol)
		if e2 != nil {
			return pkgerrors.Wrap(err, "failed to parse source policy")
		}
	}

	so.SourcePolicy = &pol
	return nil
}

func withSocketProxies(t *testing.T, proxies []socketprovider.ProxyConfig) func(*client.SolveOpt) {
	return func(so *client.SolveOpt) {
		t.Helper()
		if len(proxies) == 0 {
			return
		}

		handler, err := socketprovider.NewProxyHandler(proxies)
		assert.NilError(t, err)
		so.Session = append(so.Session, handler)
	}
}

func withDockerAuth(so *client.SolveOpt) {
	so.Session = append(so.Session, &authProvider{})
}

type authProvider struct{}

func (ap *authProvider) FetchToken(ctx context.Context, req *auth.FetchTokenRequest) (rr *auth.FetchTokenResponse, err error) {
	return nil, status.Error(codes.Unimplemented, "token fetch not supported")
}

func (ap *authProvider) Credentials(ctx context.Context, req *auth.CredentialsRequest) (*auth.CredentialsResponse, error) {
	host := strings.TrimSpace(req.GetHost())
	if host == "" {
		return &auth.CredentialsResponse{}, nil
	}

	resolved := dockercfg.ResolveRegistryHost(host)
	username, secret, err := dockercfg.GetRegistryCredentials(resolved)
	if err != nil {
		if errors.Is(err, dockercfg.ErrCredentialsMissingServerURL) {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Errorf(codes.Internal, "get credentials for %s: %v", resolved, err)
	}

	return &auth.CredentialsResponse{
		Username: username,
		Secret:   secret,
	}, nil
}

func (ap *authProvider) GetTokenAuthority(ctx context.Context, req *auth.GetTokenAuthorityRequest) (*auth.GetTokenAuthorityResponse, error) {
	return nil, status.Error(codes.Unimplemented, "token authority not supported")
}

func (ap *authProvider) VerifyTokenAuthority(ctx context.Context, req *auth.VerifyTokenAuthorityRequest) (*auth.VerifyTokenAuthorityResponse, error) {
	return nil, status.Error(codes.Unimplemented, "token authority not supported")
}

func (ap *authProvider) Register(server *grpc.Server) {
	auth.RegisterAuthServer(server, ap)
}
