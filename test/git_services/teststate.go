package gitservices

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"iter"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/identity"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
	"gotest.tools/v3/assert"
)

const (
	waitOnlineTimeout = 20 * time.Second
	customScriptDir   = "/tmp/dalec/internal/scripts"
)

// `TestState` is a bundle of stuff that the tests need access to in order to do their work.
type TestState struct {
	T    *testing.T
	Attr *Attributes

	client gwclient.Client
}

func NewTestState(t *testing.T, client gwclient.Client, attr *Attributes) TestState {
	if attr.tag == "" {
		attr.tag = identity.NewID()
	}
	return TestState{
		T:      t,
		client: client,
		Attr:   attr,
	}
}

func (ts *TestState) Client() gwclient.Client {
	if ts.client == nil {
		ts.T.Fatal("TestState: called Client() with nil client")
	}

	return ts.client
}

// Dalec spec boilerplate
func (ts *TestState) GenerateSpec(gomodContents string, auth dalec.GomodGitAuth) *dalec.Spec {
	const sourceName = "gitauth"
	var port string

	switch {
	case auth.Token != "":
		port = ts.Attr.HTTPPort
	case auth.SSH != nil:
		port = ts.Attr.SSHPort
	default:
		ts.T.Fatal("cannot tell which kind of spec is needed, aborting")
	}

	spec := &dalec.Spec{
		Name: "gomod-git-auth",
		Sources: map[string]dalec.Source{
			sourceName: {
				Inline: &dalec.SourceInline{
					Dir: &dalec.SourceInlineDir{
						Files: map[string]*dalec.SourceInlineFile{
							"go.mod": {
								Contents: string(gomodContents),
							},
						},
					},
				},
				Generate: []*dalec.SourceGenerator{
					{
						Gomod: &dalec.GeneratorGomod{
							Auth: map[string]dalec.GomodGitAuth{
								fmt.Sprintf("%s:%s", ts.Attr.PrivateGomoduleHost, port): auth,
							},
						},
					},
				},
			},
		},
	}
	return spec
}

// ServerResult contains the result of starting a git server.
type ServerResult struct {
	// IP is the IP address of the container running the server.
	IP string
	// Port is the port the server is listening on.
	Port string
	// ErrChan receives errors from the server process.
	ErrChan <-chan error
}

// `StartHTTPGitServer` starts a git HTTP server to serve the private go module
// as a git repo. It returns the container's IP address and an error channel.
func (ts *TestState) StartHTTPGitServer(ctx context.Context, gitHost llb.State) ServerResult {
	t := ts.T

	cont := ts.newContainer(ctx, gitHost)

	// Run the HTTP server binary with "serve" subcommand - it outputs JSON ready event on stdout
	proc := ts.runContainerWithEventsDirect(ctx, cont, []string{
		ts.Attr.HTTPServerPath,
		"serve",
		ts.Attr.ServerRoot,
		ts.Attr.HTTPPort,
	})

	t.Log("waiting for http server to come online")
	ready := ts.waitForReady(ctx, proc, waitOnlineTimeout)

	t.Logf("http server is online at %s:%s", ready.IP, ready.Port)

	return ServerResult{IP: ready.IP, Port: ready.Port, ErrChan: proc.ErrChan()}
}

// `buildHTTPGitServer` builds the Git HTTP server helper program.
func (ts *TestState) buildHTTPGitServer(ctx context.Context) llb.State {
	goModCache := llb.AddMount("/go/pkg/mod", llb.Scratch(), llb.AsPersistentCacheDir("/go/pkg/mod", llb.CacheMountShared))
	goBuildCache := llb.AddMount("/root/.cache/go-build", llb.Scratch(), llb.AsPersistentCacheDir("/root/.cache/go-build", llb.CacheMountShared))

	dctx := dalec.Source{
		Context: &dalec.SourceContext{
			Name: dockerui.DefaultLocalNameContext,
		},
	}

	platform := platforms.DefaultSpec()
	sOpt, err := frontend.SourceOptFromClient(ctx, ts.client, &platform)
	assert.NilError(ts.T, err)

	src, mountOpts := dctx.ToMount(sOpt)

	return llb.Image("golang:1.25", llb.WithMetaResolver(ts.client)).
		Run(
			llb.Args([]string{"go", "build", "-o=/build/out/git_http_server", "./test/git_services/cmd/server"}),
			llb.AddEnv("CGO_ENABLED", "0"),
			goModCache,
			goBuildCache,
			llb.Dir("/build/src"),
			llb.AddMount("/build/src", src, append(mountOpts, llb.Readonly)...),
		).AddMount("/build/out", llb.Scratch())
}

func (ts *TestState) newContainer(ctx context.Context, rootfs llb.State) gwclient.Container {
	t := ts.T
	client := ts.Client()

	httpServerBin := ts.buildHTTPGitServer(ctx)
	mounts := []gwclient.Mount{
		{Dest: "/", Ref: ts.stateToRef(ctx, rootfs)},
		{Dest: ts.Attr.HTTPServerPath, Selector: filepath.Base(ts.Attr.HTTPServerPath), Ref: ts.stateToRef(ctx, httpServerBin)},
	}

	cont, err := client.NewContainer(ctx, gwclient.NewContainerRequest{
		Mounts: mounts,
	})
	if err != nil {
		t.Fatalf("could not create container: %s", err)
	}

	t.Cleanup(func() {
		cont.Release(context.WithoutCancel(ctx)) //nolint:errcheck
	})

	return cont
}

func (ts *TestState) CustomFile(f File) llb.StateOption {
	dir := filepath.Dir(f.Location)

	pg := dalec.ProgressGroup("injecting custom file " + f.Location)

	return func(s llb.State) llb.State {
		return s.File(
			llb.Mkdir(dir, 0o777, llb.WithParents(true)).
				Mkfile(f.Location, 0o666, f.Inject(ts.T, ts.Attr)),
			pg,
		)
	}
}

func (ts *TestState) customScript(s Script) llb.StateOption {
	dir := customScriptDir
	absPath := filepath.Join(dir, s.Basename)

	pg := dalec.ProgressGroup("injecting custom script " + s.Basename)

	return func(worker llb.State) llb.State {
		return worker.File(
			llb.Mkdir(dir, 0o755, llb.WithParents(true)).
				Mkfile(absPath, 0o755, s.Inject(ts.T, ts.Attr)),
			pg,
		)
	}
}

// startSSHServer starts an sshd instance in a container hosting the git repo.
// It runs asynchronously and returns the container's IP and an error channel.
func (ts *TestState) StartSSHServer(ctx context.Context, gitHost llb.State) ServerResult {
	t := ts.T

	// This script uses the git_http_server getip subcommand to get the container's IP,
	// then runs an ssh server. Rather than create a new user, we will permit root login
	// to simplify things. It is running in a container so this should not be a security issue.
	serverScript := Script{
		Basename: "start_ssh_server.sh",
		Template: `
            #!/usr/bin/env sh
            set -e

            # Make sure we trap exit signals
            # This is POSIX shell, not bash, so we can't use the "EXIT" shorthand
            trap exit INT HUP TERM

            IP=$({{ .HTTPServerPath }} getip)
            PORT="{{ .SSHPort }}"

            # Generate host keys (redirect to stderr to keep stdout clean for JSON)
            ssh-keygen -A >&2

            # Start sshd in the background
            /usr/sbin/sshd -o PermitRootLogin=yes -p "$PORT" -D &
            SERVER_PID=$!

            # Wait for server to be ready
            while ! nc -zw5 "${IP}" "$PORT" 2>/dev/null; do
                # Check if server is still running
                if ! kill -0 $SERVER_PID 2>/dev/null; then
                    echo '{"type":"error","error":{"message":"sshd exited unexpectedly"}}'
                    exit 1
                fi
                sleep 0.1
            done

            # Output ready event as JSON
            printf '{"type":"ready","ready":{"ip":"%s","port":"%s"}}\n' "$IP" "$PORT"

            # Wait for server process
            wait $SERVER_PID
        `,
	}

	gitHost = gitHost.
		With(ts.customScript(serverScript))

	cont := ts.newContainer(ctx, gitHost)
	proc := ts.runContainerWithEvents(ctx, cont, serverScript)

	t.Log("waiting for ssh server to come online")
	ready := ts.waitForReady(ctx, proc, waitOnlineTimeout)

	t.Logf("ssh server is online at %s:%s", ready.IP, ready.Port)

	return ServerResult{IP: ready.IP, Port: ready.Port, ErrChan: proc.ErrChan()}
}

var (
	errContainerNoStart = errors.New("could not start server container")
	errContainerFailed  = errors.New("container process failed")
)

type bufCloser struct {
	*bytes.Buffer
}

func (b *bufCloser) Close() error {
	return nil
}

// containerProcess wraps a running container and provides an iterator over JSON events from stdout.
type containerProcess struct {
	t       *testing.T
	decoder *json.Decoder
	stdoutR *io.PipeReader
	errChan <-chan error
}

// Events returns an iterator over server events from the container's stdout.
// It yields (event, nil) on success or (zero, err) on decode error.
func (cp *containerProcess) Events() iter.Seq2[ServerEvent, error] {
	return func(yield func(ServerEvent, error) bool) {
		for {
			var event ServerEvent
			err := cp.decoder.Decode(&event)
			if err != nil {
				if err != io.EOF {
					yield(ServerEvent{}, err)
				}
				return
			}
			if !yield(event, nil) {
				return
			}
		}
	}
}

// ErrChan returns a channel that receives container process errors.
func (cp *containerProcess) ErrChan() <-chan error {
	return cp.errChan
}

// runContainerWithEvents runs a container and returns a containerProcess for reading events.
func (ts *TestState) runContainerWithEvents(ctx context.Context, cont gwclient.Container, s Script) *containerProcess {
	return ts.runContainerWithEventsDirect(ctx, cont, []string{s.absPath()})
}

// runContainerWithEventsDirect runs a container with the given args and returns a containerProcess.
func (ts *TestState) runContainerWithEventsDirect(ctx context.Context, cont gwclient.Container, args []string) *containerProcess {
	var (
		t = ts.T
	)

	stdoutR, stdoutW := io.Pipe()
	stderr := bufCloser{bytes.NewBuffer(nil)}

	t.Log("starting container")
	cp, err := cont.Start(ctx, gwclient.StartRequest{
		Args:   args,
		Stdout: stdoutW,
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatal(errors.Join(errContainerNoStart, err))
	}

	ec := make(chan error, 1)

	// Wait for container to exit
	go func() {
		err := cp.Wait()
		if err != nil {
			err = errors.Join(errContainerFailed, err, fmt.Errorf("stderr:\n%s", stderr.String()))
			ec <- err
			stdoutW.CloseWithError(err)
		} else {
			stdoutW.Close()
		}
	}()

	return &containerProcess{
		t:       t,
		decoder: json.NewDecoder(stdoutR),
		stdoutR: stdoutR,
		errChan: ec,
	}
}

// waitForReady waits for a Ready event from the container process.
func (ts *TestState) waitForReady(ctx context.Context, proc *containerProcess, timeout time.Duration) *ReadyEvent {
	t := ts.T
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Close the pipe on timeout to unblock the decoder
	go func() {
		<-ctx.Done()
		proc.stdoutR.CloseWithError(ctx.Err())
	}()

	for event, err := range proc.Events() {
		if err != nil {
			t.Fatalf("error reading event: %v", err)
		}

		switch event.Type {
		case EventTypeReady:
			if event.Ready == nil {
				t.Fatal("received ready event with nil data")
			}
			return event.Ready
		case EventTypeError:
			if event.Error != nil {
				t.Fatalf("server error: %s", event.Error.Message)
			}
		case EventTypeLog:
			if event.Log != nil {
				t.Logf("server: %s", event.Log.Message)
			}
		}
	}

	if ctx.Err() != nil {
		t.Fatalf("timeout waiting for server ready event")
	}
	t.Fatal("event stream closed before receiving ready event")
	return nil
}

// `runScript` is a replacement for `llb.State.Run(...)`. It mounts the
// specified script in the custom script directory, then generates the llb to
// run the script on `worker`.
func (ts *TestState) runScriptOn(worker llb.State, s Script, runopts ...llb.RunOption) llb.ExecState {
	worker = worker.With(ts.customScript(s))
	o := []llb.RunOption{
		llb.Args([]string{s.absPath()}),
		dalec.ProgressGroup("running script " + s.Basename),
	}

	o = append(o, runopts...)
	return worker.Run(o...)
}

// InitializeGitRepo returns a stateOption that uses `worker` to create an
// initialized git repository from the base state.
func (ts *TestState) InitializeGitRepo(worker llb.State) llb.StateOption {
	attr := ts.Attr

	repoScript := Script{
		Basename: "git_init.sh",
		Template: `
            #!/usr/bin/env sh

            set -ex
            export GIT_CONFIG_NOGLOBAL=true
            git init
            git config user.name foo
            git config user.email foo@bar.com

            git add -A
            git commit -m commit --no-gpg-sign
            git tag {{ .PrivateGoModuleGitTag }}
`,
	}

	return func(repo llb.State) llb.State {
		worker = worker.Dir(ts.Attr.PrivateRepoAbsPath())

		return ts.runScriptOn(worker, repoScript).
			AddMount(attr.RepoAbsDir(), repo)
	}
}

// `UpdatedGitconfig` updatesd the gitconfig on the gomod worker. This is
// convoluted, but necessary. The `go` tool uses `git` under the hood to
// download go modules in response to an invocation of `go mod download`.
// Making such an invocation will cause go to open the `go.mod` file in the
// current directory and build a dependency graph of modules to download.
//
// A go module cannot have a URI that includes a port number. Go uses the
// standard HTTP/HTTPS/SSH ports of 80, 443, and 22 respenctively, to attempt
// to fetch a module. Root privileges would be required to bind to those port
// numbers, so we run our HTTP and SSH servers on nonstandard ports.
//
// Modifying the gitconfig as below will tell git to substitute
// http://host.com:port/ when it receives a request for a repository at
// http://host.com/ . That way, when go sees a module with URI path
// `host.com/module/name`, it will call `git` to look up the repository there.
// Git will first consult the gitconfig to see if there are any subsittutions,
// and will then make a request instead to http://host.com:<portnumber>/module/name .
func (ts *TestState) UpdatedGitconfig() llb.StateOption {
	s := Script{
		Basename: "update_gitconfig.sh",
		Template: `
            git config --global "url.http://{{ .PrivateGomoduleHost }}:{{ .HTTPPort }}.insteadOf" "https://{{ .PrivateGomoduleHost }}"
            git config --global credential."http://{{ .PrivateGomoduleHost }}:{{ .HTTPPort }}.helper" "/usr/local/bin/frontend credential-helper --kind=token"
        `,
	}

	return func(st llb.State) llb.State {
		return ts.runScriptOn(st, s).Root()
	}
}
func (ts *TestState) stateToRef(ctx context.Context, st llb.State) gwclient.Reference {
	t := ts.T

	def, err := st.Marshal(ctx)
	if err != nil {
		t.Fatalf("could not marshal git repo llb: %s", err)
	}

	res, err := ts.Client().Solve(ctx, gwclient.SolveRequest{Definition: def.ToPB()})
	if err != nil {
		t.Fatalf("could not solve git repo llb %s", err)
	}

	ref, err := res.SingleRef()
	if err != nil {
		t.Fatalf("could not convert result to single ref %s", err)
	}
	return ref
}

// Wrapper types to make templating and injecting files into llb states
type File struct {
	Location string
	Template string
}

// Wrapper types to make templating and injecting files into llb states.
// Scripts will typically be copied into `customScriptDir`
type Script struct {
	Basename string
	Template string
}

func (s *Script) absPath() string {
	return filepath.Join(customScriptDir, s.Basename)
}

// Completes a template and adds a shebang to a script.
func (s *Script) Inject(t *testing.T, obj *Attributes) []byte {
	tmpl := "#!/usr/bin/env sh\n" + s.Template
	f := File{
		Template: tmpl,
	}

	return f.Inject(t, obj)
}

func (f *File) Inject(t *testing.T, obj *Attributes) []byte {
	cleaned := cleanWhitespace(f.Template)

	if obj == nil {
		return []byte(cleaned)
	}

	tmpl, err := template.New("depending go mod").Parse(cleaned)
	if err != nil {
		t.Fatalf("could not parse template: %s", err)
	}

	var contents bytes.Buffer
	if err := tmpl.Execute(&contents, obj); err != nil {
		t.Fatalf("could not inject values into template: %s", err)
	}

	return contents.Bytes()
}

// Removes unnecessary whitespace so that scripts run properly and don't have a
// messed-up shebang.
func cleanWhitespace(s string) string {
	var b bytes.Buffer

	tb := bytes.NewBuffer([]byte(s))
	sc := bufio.NewScanner(tb)

	initial := true
	for sc.Scan() {
		t := strings.TrimSpace(sc.Text())

		if initial && t == "" {
			initial = false
			continue
		}

		b.WriteString(t)
		b.WriteRune('\n')
	}

	return b.String()
}
