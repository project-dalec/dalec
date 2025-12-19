package test

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/test/cmd/git_repo/passwd"
	gitservices "github.com/project-dalec/dalec/test/git_services"
	"github.com/project-dalec/dalec/test/testenv"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

func TestGomodGitAuth(t *testing.T) {
	t.Parallel()
	ctx := startTestSpan(baseCtx, t)

	// In order to perform these tests, we need to run auxiliary services to
	// act as git servers that host private git modules. Rather than create a
	// separate virtual network, we are using host networking to accomplish
	// this. In order to do so, the buildx instance we use to run the tests
	// needs to have host networking enabled; otherwise, the solve request will
	// fail.
	netHostBuildxEnv := testenv.NewWithNetHostBuildxInstance(ctx, t)

	secretName := "super-secret"
	sshID := "dalecssh"

	attr := gitservices.Attributes{
		ServerRoot:             "/",
		PrivateRepoPath:        "username/private",
		DependingRepoPath:      "username/public",
		HTTPServerPath:         "/usr/local/bin/git_http_server",
		PrivateGomoduleHost:    "host.docker.internal",
		GitRemoteAddr:          "127.0.0.1",
		HTTPPort:               findRandomAvailablePort(t),
		SSHPort:                findRandomAvailablePort(t),
		HTTPServerBuildDir:     "/tmp/dalec/internal/dalec_coderoot",
		HTTPServeCodeLocalPath: "./test/cmd/git_repo",
		OutDir:                 "/tmp/dalec/internal/output",
		ModFileGoVersion:       "1.24.6",
	}

	// This is the go.mod file of the go module that *depends on our private go
	// module*. We call it the "depending" go module.
	dependingModfile := gitservices.File{
		Template: `
module {{ .PrivateGomoduleHost }}/{{ .DependingRepoPath }}

go {{ .ModFileGoVersion }}

require {{ .PrivateGomoduleHost }}/{{ .PrivateRepoPath }}.git {{ .PrivateGoModuleGitTag }}
`,
	}

	// This is the go.mod file of the *private go module*.
	dependentModfile := gitservices.File{
		Location: "go.mod",
		Template: `
module {{ .PrivateGomoduleHost }}/{{ .PrivateRepoPath }}.git

go {{ .ModFileGoVersion }}
            `,
	}

	// This closure creates the necessary llb states representing:
	//   - the `worker`, which will be the worker on which the `debug/gomods`
	//     target is run
	//   - the `repo`, which is the initialized git repository containing the
	//     private go module
	//  - the `gitHost`, which is essentially the worker plus the repo: an OS
	//    capable of acting as a git server to host the private repo, which is
	//    copied in.
	initStates := func(ts *gitservices.TestState) (llb.State, llb.State, llb.State) {
		worker := initWorker(ts.Client())
		repo := llb.Scratch().
			With(ts.CustomFile(dependentModfile)).
			With(ts.CustomFile(gitservices.File{
				Location: "foo",
				Template: "bar\n",
			})).
			With(ts.InitializeGitRepo(worker))

		gitHost := worker.With(hostedRepo(repo, ts.Attr.RepoAbsDir()))

		return worker, repo, gitHost
	}

	// This generates the contents of the depending mod file, using `t.Fatal`
	// in the case of failures and using the git tag and other attributes from
	// `attr`.
	depModfileContents := func(t *testing.T, attr *gitservices.Attributes) string {
		return string(dependingModfile.Inject(t, attr))
	}

	t.Run("HTTP", func(t *testing.T) {
		t.Parallel()

		opts := []testenv.TestRunnerOpt{
			// This gives buildkit access to a secret with the name
			// `secretName` and the value `passwd.Password`. On the worker that
			// fetches the gomod dependencies, a file will be mounted at the
			// location `/run/secrets/super-secret` with the contents
			// `passwd.Password`.
			testenv.WithSecrets(secretName, passwd.Password),
			// Host Networking MUST also be requested on the individual solve
			// requests. It is necessary but not sufficient for the buildx
			// instance to have host networking enabled.
			testenv.WithHostNetworking,
		}

		netHostBuildxEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			// This MUST be called at the  start of each test. Because we
			// persist the go mod cache between runs, the git tag of the
			// private go module needs to be unique. Within a single run of the
			// tests, the http and ssh tests cannot have the same tag or we
			// risk a false positive due to caching.
			attr := attr.WithNewPrivateGoModuleGitTag()

			testState := gitservices.NewTestState(ctx, t, client, attr)

			worker, _, gitHost := initStates(&testState)
			httpGitHost := gitHost.With(testState.UpdatedGitconfig())
			httpErrChan := testState.StartHTTPGitServer(httpGitHost)

			// Generate a basic spec file with the *depending* go module's
			// go.mod file inlined, and the name of the secret corresponding to
			// the password required to authenticate to the git repository.
			spec := testState.GenerateSpec(depModfileContents(t, attr), dalec.GomodGitAuth{
				Token: secretName,
			})

			sr := newSolveRequest(
				withBuildTarget("debug/gomods"),
				withSpec(ctx, t, spec),
				withExtraHost(testState.Attr.PrivateGomoduleHost, testState.Attr.GitRemoteAddr),
				// We need to provide a custom worker to the gomod generator.
				// The reason is described in the documentation to
				// `updatedGitconfig`
				withBuildContext(ctx, t, "gomod-worker", worker.With(testState.UpdatedGitconfig())),
			)

			solveResultChan := make(chan *gwclient.Result)
			solveErrChan := make(chan error)
			solveTCh(ctx, t, testState.Client(), sr, solveResultChan, solveErrChan)

			var res *gwclient.Result
			select {
			case err := <-httpErrChan:
				t.Fatalf("http server unexpectedly failed: %s", err)
			case err := <-solveErrChan:
				t.Fatalf("solve failed: %s", err)
			case r := <-solveResultChan:
				res = r
			}

			filename := calculateFilename(ctx, t, attr, res)
			checkFile(ctx, t, filename, res, []byte("bar\n"))
		}, opts...)
	})

	t.Run("SSH", func(t *testing.T) {
		t.Parallel()

		sockaddr := getSocketAddr(t)

		// In order to simulate real-life SSH auth scenarios, we generate a
		// keypair. Buildkit handles SSH auth by forwarding an SSH agent socket
		// to provide the private key upon request. We start an agent here to
		// serve the private key.
		pubkey, privkey := generateKeyPair(t)
		agentErrChan := startSSHAgent(t, privkey, sockaddr)

		opts := []testenv.TestRunnerOpt{
			// This tells buildkit to forward the SSH Agent socket, giving the
			// gomod generator worker access to the private key
			testenv.WithSSHSocket(sshID, sockaddr),
			testenv.WithHostNetworking,
		}
		netHostBuildxEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			// This MUST be called at the  start of each test. Because we
			// persist the go mod cache between runs, the git tag of the
			// private go module needs to be unique. Within a single run of the
			// tests, the http and ssh tests cannot have the same tag or we
			// risk a false positive due to caching.
			attr := attr.WithNewPrivateGoModuleGitTag()

			testState := gitservices.NewTestState(ctx, t, client, attr)

			_, repo, gitHost := initStates(&testState)
			sshGitHost := gitHost.
				// The SSH host has to know the public key that corresponds to
				// the client's private key; otherwise it will deny access.
				With(authorizedKey(pubkey, "/root")).
				// In order to host a git repo over SSH, it needs to be hosted
				// as a "bare" git repo. An existing git repo can be made into
				// a bare repo by hoisting the contents of the `.git` directory
				// up a level, removing everything else, and calling `git init
				// --bare`. That bare repo can then be used as a git remote
				// over SSH.
				With(bareRepo(repo, attr.RepoAbsDir()))

			const githostUsername = "root"
			sshErrChan := testState.StartSSHServer(sshGitHost)

			spec := testState.GenerateSpec(depModfileContents(t, attr), dalec.GomodGitAuth{
				SSH: &dalec.GomodGitAuthSSH{
					ID:       sshID,
					Username: githostUsername,
				},
			})
			sr := newSolveRequest(
				withBuildTarget("debug/gomods"),
				withSpec(ctx, t, spec),
				// The extra host in fquestion here is the
				withExtraHost(testState.Attr.PrivateGomoduleHost, testState.Attr.GitRemoteAddr),
			)

			solveResultChan := make(chan *gwclient.Result)
			solveErrChan := make(chan error)

			solveTCh(ctx, t, testState.Client(), sr, solveResultChan, solveErrChan)

			var res *gwclient.Result
			select {
			case err := <-agentErrChan:
				t.Fatalf("ssh agent unexpededly failed: %s", err)
			case err := <-sshErrChan:
				t.Fatalf("ssh server unexpectedly failed: %s", err)
			case err := <-solveErrChan:
				t.Fatalf("solve failed: %s", err)
			case r := <-solveResultChan:
				res = r
			}

			filename := calculateFilename(ctx, t, attr, res)
			checkFile(ctx, t, filename, res, []byte("bar\n"))
		}, opts...)
	})
}

func getSocketAddr(t *testing.T) string {
	dir := t.TempDir()
	sockaddr := filepath.Join(dir, "ssh.agent.sock")

	return sockaddr
}

// The go module, once downloaded, will have a randomized filename so we need
// to find out what it is before we can check the contents of the file inside.
func calculateFilename(ctx context.Context, t *testing.T, attr *gitservices.Attributes, res *gwclient.Result) string {
	outDirBase := filepath.Join(attr.PrivateGomoduleHost, filepath.Dir(attr.PrivateRepoPath))
	modDir := getDirName(ctx, t, res, outDirBase, "private.git@*")
	filename := filepath.Join(outDirBase, modDir, "foo")
	return filename
}

// Overwrites/creates the `.ssh/authorized_keys` file in the specified
// `homedir` with the provided `pubkey` in the proper format.
func authorizedKey(pubkey ssh.PublicKey, homedir string) llb.StateOption {
	dir := filepath.Join(homedir, ".ssh")
	const basename = "authorized_keys"
	absPath := filepath.Join(dir, basename)

	pubkeyData := ssh.MarshalAuthorizedKey(pubkey)

	return func(s llb.State) llb.State {
		return s.File(
			llb.Mkdir(dir, 0o700, llb.WithParents(true)).
				Mkfile(absPath, 0o600, pubkeyData),
		)
	}
}

// Copies the repo to its mountpoint.
func hostedRepo(repo llb.State, mountpoint string) llb.StateOption {
	return func(worker llb.State) llb.State {
		return worker.File(
			llb.Mkdir(mountpoint, 0o755, llb.WithParents(true)).
				Copy(repo, "/", mountpoint),
		)
	}
}

// `bareRepo` initializes a bare git repo from `repo`.
func bareRepo(repo llb.State, mountpoint string) llb.StateOption {
	return func(worker llb.State) llb.State {
		bare := llb.Scratch().File(
			llb.Copy(repo, ".git", "/", &llb.CopyInfo{
				CopyDirContentsOnly: true,
			}),
		)

		bare = worker.Run(dalec.ShArgs("git init --bare")).AddMount(mountpoint, bare)

		return worker.User("git").File(
			llb.Rm(mountpoint).
				Mkdir(mountpoint, 0o755, llb.WithParents(true)).
				Copy(bare, "/", mountpoint),
		)
	}
}

// `startSSHAgent` creates an SSH agent on `sockaddr`, loaded up with
// `privkey`. The agent is started in the background and errors will be
// provided on the returned channel.
func startSSHAgent(t *testing.T, privkey crypto.PrivateKey, sockaddr string) <-chan error {
	ec := make(chan error)
	t.Cleanup(func() {
		close(ec)
	})

	kr := agent.NewKeyring()
	if err := kr.Add(agent.AddedKey{
		PrivateKey: privkey,
	}); err != nil {
		t.Fatalf("could not add private key to agent keyring: %s", err)
	}

	t.Logf("starting ssh agent on socket %s", sockaddr)
	listener, err := net.Listen("unix", sockaddr)
	if err != nil {
		t.Fatalf("can't listen on unix socket: %s", err)
	}

	go func() {
		for {
			c, err := listener.Accept()
			t.Log("connection accepted")
			if err != nil {
				ec <- fmt.Errorf("listener.Accept: %w", err)
				return
			}

			go func() {
				if err := agent.ServeAgent(kr, c); err != nil {
					if errors.Is(err, io.EOF) {
						return
					}

					ec <- fmt.Errorf("cannot serve agent: %w", err)
				}
			}()
		}
	}()

	return ec
}

func generateKeyPair(t *testing.T) (ssh.PublicKey, crypto.PrivateKey) {
	u, privkey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("could not generate ssh keypair: %s", err)
	}

	pubkey, err := ssh.NewPublicKey(u)
	if err != nil {
		t.Fatalf("could not parse ssh public key: %s", err)
	}

	return pubkey, privkey
}

// This is a helper function to find the filename of the download go module
// file. The containing directory will have a randomized filename.
func getDirName(ctx context.Context, t *testing.T, res *gwclient.Result, base, dirPattern string) string {
	ref, err := res.SingleRef()
	if err != nil {
		t.Fatal(err)
	}

	stats, err := ref.ReadDir(ctx, gwclient.ReadDirRequest{
		Path:           base,
		IncludePattern: dirPattern,
	})

	if err != nil {
		t.Fatal(err)
	}

	if len(stats) == 0 {
		t.Fatalf("private go module directory not found")
	}

	return stats[0].Path
}

func initWorker(c gwclient.Client) llb.State {
	worker := llb.Image("alpine:latest", llb.Platform(ocispecs.Platform{Architecture: runtime.GOARCH, OS: "linux"}), llb.WithMetaResolver(c)).
		Run(llb.Shlex("apk add --no-cache go git ca-certificates patch openssh netcat-openbsd")).Root()
	return worker
}

func findRandomAvailablePort(t *testing.T) string {
	addr, err := net.ResolveTCPAddr("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}

	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func(t *testing.T) {
		_ = l.Close() // if we got the port, ignore failure to close
	}(t)

	tcpa, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("extpeccted return value of l.Addr() to be a (*net.TCPAddr)")
	}

	p := tcpa.Port
	return strconv.Itoa(p)
}

func withExtraHost(host string, ipv4 string) func(cfg *newSolveRequestConfig) {
	return func(cfg *newSolveRequestConfig) {
		const addHostsKey = "add-hosts"
		r := cfg.req

		if r.FrontendOpt == nil {
			r.FrontendOpt = make(map[string]string)
		}

		var prefix string
		if existing, ok := r.FrontendOpt[addHostsKey]; ok {
			prefix = existing + ","
		}

		r.FrontendOpt[addHostsKey] = fmt.Sprintf("%s%s=%s", prefix, host, ipv4)
	}
}
