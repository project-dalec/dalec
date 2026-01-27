package gitservices

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/test/cmd/git_repo/build"
)

const (
	waitOnlineTimeout = 20 * time.Second
	customScriptDir   = "/tmp/dalec/internal/scripts"
)

// `TestState` is a bundle of stuff that the tests need access to in order to do their work.
type TestState struct {
	T    *testing.T
	Ctx  context.Context
	Attr Attributes

	client gwclient.Client
}

func NewTestState(ctx context.Context, t *testing.T, client gwclient.Client, attr *Attributes) TestState {
	return TestState{
		T:      t,
		Ctx:    ctx,
		client: client,
		Attr:   *attr,
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

// `StartHTTPGitServer` starts a git HTTP server to serve the private go module
// as a git repo.
func (ts *TestState) StartHTTPGitServer(gitHost llb.State) <-chan error {
	t := ts.T

	serverScript := Script{
		Basename: "run_http_server.sh",
		Template: `
            #!/usr/bin/env sh

            set -ex
            exec {{ .HTTPServerPath }} {{ .ServerRoot }} {{ .GitRemoteAddr }} {{ .HTTPPort }}
        `,
	}

	// This script attempts to connect to the http server. The `nc -z` flag
	// discconnects and exits with status 0 if a successful connection is made.
	// `nc -w5` gives up and exits with status 1 after a 5-second timeout.
	waitScript := Script{
		Basename: "wait_for_http.sh",
		Template: `
            #!/usr/bin/env sh
            while ! nc -zw5 "{{ .PrivateGomoduleHost }}" "{{ .HTTPPort }}"; do
                sleep 0.1
            done
        `,
	}

	// The Git HTTP server is coded in a separate program at test/cmd/git_repo.
	// We need to build and inject it.
	httpServerBin := ts.buildHTTPGitServer()

	gitHost = gitHost.
		With(ts.customScript(serverScript)).
		With(ts.customScript(waitScript)).
		File(
			llb.Copy(httpServerBin, "/", ts.Attr.HTTPServerDir()),
		)

	cont := ts.newContainer(gitHost)

	env := ts.getStateEnv(gitHost)
	errChan := ts.runContainer(cont, env, serverScript)

	t.Log("waiting for http server to come online")

	timeout := waitOnlineTimeout
	ts.runWaitScript(cont, env, waitScript, timeout)

	t.Logf("http server is online")

	return errChan
}

// `buildHTTPGitServer` builds the Git HTTP server helper program.
func (ts *TestState) buildHTTPGitServer() llb.State {
	var (
		t      = ts.T
		ctx    = ts.Ctx
		client = ts.Client()
	)

	s, err := build.HTTPGitServer(ctx, client)
	if err != nil {
		t.Fatalf("could not build http git server: %s", err)
	}

	if s == nil {
		t.Fatalf("fatal: http git server state was nil")
	}

	return *s
}

type CustomMount struct {
	dst string
	st  llb.State
}

func (ts *TestState) newContainer(rootfs llb.State, extraMounts ...CustomMount) gwclient.Container {
	t := ts.T
	ctx := ts.Ctx
	client := ts.Client()
	attr := ts.Attr

	mountCfgs := []CustomMount{
		{
			dst: "/",
			st:  rootfs,
		},
	}
	mountCfgs = append(mountCfgs, extraMounts...)

	mounts := make([]gwclient.Mount, 0, len(mountCfgs))
	for _, cm := range mountCfgs {
		mounts = append(mounts, gwclient.Mount{
			Dest: cm.dst,
			Ref:  ts.stateToRef(cm.st),
		})
	}

	cont, err := client.NewContainer(ctx, gwclient.NewContainerRequest{
		Mounts:  mounts,
		NetMode: pb.NetMode_HOST,
		ExtraHosts: []*pb.HostIP{
			{
				Host: attr.PrivateGomoduleHost,
				IP:   attr.GitRemoteAddr,
			},
		},
	})
	if err != nil {
		t.Fatalf("could not create ssh server container: %s", err)
	}

	return cont
}
func (ts *TestState) CustomFile(f File) llb.StateOption {
	dir := filepath.Dir(f.Location)

	return func(s llb.State) llb.State {
		return s.File(
			llb.Mkdir(dir, 0o777, llb.WithParents(true)).
				Mkfile(f.Location, 0o666, f.Inject(ts.T, &ts.Attr)),
		)
	}
}
func (ts *TestState) customScript(s Script) llb.StateOption {
	dir := customScriptDir
	absPath := filepath.Join(dir, s.Basename)

	return func(worker llb.State) llb.State {
		return worker.File(
			llb.Mkdir(dir, 0o755, llb.WithParents(true)).
				Mkfile(absPath, 0o755, s.Inject(ts.T, &ts.Attr)),
		)
	}
}

// startSSHServer starts an sshd instance in a container hosting the git repo.
// It runs asynchronously and checks the connection after starting the server.
func (ts *TestState) StartSSHServer(gitHost llb.State) <-chan error {
	t := ts.T

	// This script runs an ssh server. Rather than create a new user, we will
	// permit root login to simplify things. It is running in a container so
	// this should not be a security issue.
	serverScript := Script{
		Basename: "start_ssh_server.sh",
		Template: `
            #!/usr/bin/env sh
            set -ex
            ssh-keygen -A
            exec /usr/sbin/sshd -o PermitRootLogin=yes -p {{ .SSHPort }} -D
        `,
	}

	// This script attempts to connect to the ssh server. The `nc -z` flag
	// discconnects and exits with status 0 if a successful connection is made.
	// `nc -w5` gives up and exits with status 1 after a 5-second timeout.
	waitScript := Script{
		Basename: "wait_for_ssh.sh",
		Template: `
            #!/usr/bin/env sh
            while ! nc -zw5 "{{ .PrivateGomoduleHost }}" "{{ .SSHPort }}"; do
                sleep 0.1
            done
`,
	}

	gitHost = gitHost.
		With(ts.customScript(serverScript)).
		With(ts.customScript(waitScript))

	cont := ts.newContainer(gitHost)
	env := ts.getStateEnv(gitHost)
	errChan := ts.runContainer(cont, env, serverScript)

	t.Log("waiting for ssh server to come online")

	timeout := waitOnlineTimeout
	ts.runWaitScript(cont, env, waitScript, timeout)

	t.Logf("ssh server is online")

	return errChan
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

func (ts *TestState) startContainer(cont gwclient.Container, env []string, s Script) (gwclient.ContainerProcess, bufCloser, bufCloser) {
	var (
		t   = ts.T
		ctx = ts.Ctx
	)
	stdout := bufCloser{bytes.NewBuffer(nil)}
	stderr := bufCloser{bytes.NewBuffer(nil)}

	cp, err := cont.Start(ctx, gwclient.StartRequest{
		Env:    env,
		Stdout: &stdout,
		Stderr: &stderr,
		Args:   []string{s.absPath()},
	})
	if err != nil {
		t.Fatalf("could not start server: %s\nstdout:\n%s\n===\nstderr:\n%s\n", err, stdout.String(), stderr.String())
	}

	return cp, stdout, stderr
}

// Return a new TestState with the context set to a context with a deadline.
func (ts *TestState) withTimeout(timeout time.Duration) (*TestState, func()) {
	if ts == nil {
		return ts, func() {}
	}

	ts2 := *ts

	var cancel func()
	ts2.Ctx, cancel = context.WithTimeout(ts2.Ctx, timeout)

	return &ts2, cancel
}

// `runWaitScript` runs a script checking on the just-started server (http or ssh).
func (ts *TestState) runWaitScript(cont gwclient.Container, env []string, s Script, timeout time.Duration) {
	ts2, cancel := ts.withTimeout(timeout)
	defer cancel()

	untilConnected, stdout, stderr := ts2.startContainer(cont, env, s)

	if err := untilConnected.Wait(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			ts2.T.Fatalf("Could not start server, timed out: %s", err)
		}

		ts2.T.Fatalf("could not check progress of server, container command failed: %s\nstdout:\n%s\n=====\nstderr:\n%s\n", err, stdout.String(), stderr.String())
	}
}

// runContainer runs a container in the background and sends errors to the returned channel
func (ts *TestState) runContainer(cont gwclient.Container, env []string, s Script) <-chan error {

	var (
		ctx = ts.Ctx
	)

	stdout := bufCloser{bytes.NewBuffer(nil)}
	stderr := bufCloser{bytes.NewBuffer(nil)}

	ts.T.Log("listening")
	cp, err := cont.Start(ctx, gwclient.StartRequest{
		Args:   []string{s.absPath()},
		Env:    env,
		Stdout: &stdout,
		Stderr: &stderr,
	})

	if err != nil {
		ts.T.Fatal(errors.Join(errContainerNoStart, err))
	}

	// Log but do not fail, since you cannot fail from within a goroutine
	ec := make(chan error)
	go func() {
		if err := cp.Wait(); err != nil {
			ec <- errors.Join(errContainerFailed, err, fmt.Errorf("stdout:\n%s\n=====\nstderr:\n%s", stdout.String(), stderr.String()))
		}
	}()

	return ec
}

// Returns the list of env vars from an llb.State
func (ts *TestState) getStateEnv(st llb.State) []string {
	env, err := st.Env(ts.Ctx)
	if err != nil {
		ts.T.Logf("unable to copy env: %s", err)
	}

	return env.ToArray()
}

// `runScript` is a replacement for `llb.State.Run(...)`. It mounts the
// specified script in the custom script directory, then generates the llb to
// run the script on `worker`.
func (ts *TestState) runScriptOn(worker llb.State, s Script, runopts ...llb.RunOption) llb.ExecState {
	worker = worker.With(ts.customScript(s))
	o := []llb.RunOption{
		llb.Args([]string{s.absPath()}),
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
func (ts *TestState) stateToRef(st llb.State) gwclient.Reference {
	t := ts.T
	ctx := ts.Ctx

	def, err := st.Marshal(ctx)
	if err != nil {
		t.Fatalf("could not marshal git repo llb: %s", err)
	}

	res, err := ts.Client().Solve(ts.Ctx, gwclient.SolveRequest{Definition: def.ToPB()})
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
