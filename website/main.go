package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/progress/progressui"
	"github.com/pkg/browser"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend/pkg/bkfs"
	"github.com/project-dalec/dalec/test/testenv"
	"github.com/tonistiigi/fsutil"
	"github.com/tonistiigi/fsutil/types"
)

var (
	//go:embed static src docs *.js *.ts *.json yarn.lock
	docsfs embed.FS

	//go:embed Dockerfile
	dockerfileDt []byte
)

type addrPortFlag netip.AddrPort

func (f *addrPortFlag) String() string {
	return netip.AddrPort(*f).String()
}

func (f *addrPortFlag) Set(v string) error {
	addrPort, err := netip.ParseAddrPort(v)
	if err != nil {
		return err
	}

	if !addrPort.IsValid() {
		return fmt.Errorf("invalid addr: %q", v)
	}

	*f = addrPortFlag(addrPort)
	return err
}

func (f addrPortFlag) ToAddrPort() netip.AddrPort {
	return netip.AddrPort(f)
}

func newDefaultAddrPortFlag() *addrPortFlag {
	ap := netip.MustParseAddrPort("127.0.0.1:0")
	fl := addrPortFlag(ap)
	return &fl
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	addrFl := newDefaultAddrPortFlag()
	flag.Var(addrFl, "addr", "<addr>:<port> to serve the docs site on")
	debugFl := flag.Bool("debug", false, "enable debug logging")

	flag.Parse()

	level := slog.LevelInfo
	if *debugFl {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
	}))
	slog.SetDefault(logger)

	go func() {
		<-ctx.Done()
		cancel()

		<-time.After(30 * time.Second)
		slog.Warn("force exiting after timeout")
		os.Exit(128 + int(syscall.SIGINT))
	}()

	env := testenv.New()
	client, err := env.Buildkit(ctx)
	if err != nil {
		panic(err)
	}

	if err := website(ctx, client, addrFl.ToAddrPort()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type asbuildkitFS struct {
	fs.FS
}

func (as asbuildkitFS) sanitizePath(p string) string {
	if p == "/" {
		return "."
	}
	if strings.HasPrefix(p, "/") {
		p = "." + p
	}
	return p
}

func (as asbuildkitFS) Open(name string) (io.ReadCloser, error) {
	return as.FS.Open(as.sanitizePath(name))
}

func (as asbuildkitFS) Walk(ctx context.Context, root string, fn fs.WalkDirFunc) error {
	walk := func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fn(path, d, err)
		}
		return fn(path, &statDirEntry{DirEntry: d, path: path}, nil)
	}
	return fs.WalkDir(as.FS, as.sanitizePath(root), walk)
}

// statDirEntry wraps fs.DirEntry to provide FileInfo with *types.Stat in Sys()
type statDirEntry struct {
	fs.DirEntry
	path string
}

func (s *statDirEntry) Info() (fs.FileInfo, error) {
	fi, err := s.DirEntry.Info()
	if err != nil {
		return nil, err
	}
	return &statFileInfo{FileInfo: fi, path: s.path}, nil
}

// statFileInfo wraps fs.FileInfo to return *types.Stat from Sys()
type statFileInfo struct {
	fs.FileInfo
	path string
}

func (s *statFileInfo) Sys() any {
	return &types.Stat{
		Path:    s.path,
		Mode:    uint32(s.FileInfo.Mode()),
		Size:    s.FileInfo.Size(),
		ModTime: s.FileInfo.ModTime().UnixNano(),
	}
}

func baseImage(ctx context.Context, client gwclient.Client) (llb.State, error) {
	st := llb.Scratch().File(llb.Mkfile(dockerui.DefaultDockerfileName, 0644, dockerfileDt))
	def, err := st.Marshal(ctx)
	if err != nil {
		return llb.Scratch(), err
	}
	res, err := client.Solve(ctx, gwclient.SolveRequest{
		Frontend: "dockerfile.v0",
		FrontendInputs: map[string]*pb.Definition{
			dockerui.DefaultLocalNameDockerfile: def.ToPB(),
		},
	})
	if err != nil {
		return llb.Scratch(), err
	}
	ref, err := res.SingleRef()
	if err != nil {
		return llb.Scratch(), err
	}

	return ref.ToState()
}

func websiteContext(ctx context.Context, client gwclient.Client) (llb.State, error) {
	dc, err := dockerui.NewClient(client)
	if err != nil {
		return llb.Scratch(), err
	}

	bctx, err := dc.MainContext(ctx)
	if err != nil {
		return llb.Scratch(), err
	}

	out := bctx.
		File(llb.Mkdir("/node_modules", 0o755))

	return out, nil
}

// generateSite returns a state option that generates the static website content
// using the provided toolchain.
// The input state of the state option is the content to generate the site from.
func generateSite(toolchain llb.State) llb.StateOption {
	return func(in llb.State) llb.State {
		const (
			modsCacheID = "dalec-website-node-modules"
			rootCacheID = "dalec-website-npm-root"
		)
		cacheMounts := dalec.RunOptFunc(func(ei *llb.ExecInfo) {
			llb.AddMount("/website/node_modules", llb.Scratch(), llb.AsPersistentCacheDir(modsCacheID, llb.CacheMountLocked)).SetRunOption(ei)
			llb.AddMount("/root/.npm", llb.Scratch(), llb.AsPersistentCacheDir(rootCacheID, llb.CacheMountLocked)).SetRunOption(ei)
		})
		// Install dependencies
		generated := toolchain.Run(
			cacheMounts,
			llb.Dir("/website"),
			llb.AddMount("/website", in),
			llb.Args([]string{"npm", "install"}),
			llb.WithCustomName("Install website dependencies"),
		).
			// `yarn build` generates the static site content in build/
			Run(
				cacheMounts,
				llb.Dir("/website"),
				llb.Args([]string{"yarn", "build"}),
				llb.AddEnv("DOCUSAURUS_BASE_URL", "/"),
				llb.WithCustomName("Build static website"),
			).AddMount("/website", in)

		out := llb.Scratch().File(llb.Copy(generated, "build", "/", dalec.WithDirContentsOnly()), llb.WithCustomName("Get static content"))

		return out
	}
}

func website(ctx context.Context, bkc *client.Client, addr netip.AddrPort) error {
	defer bkc.Close() //nolint:errcheck

	so := client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameContext: asbuildkitFS{docsfs},
		},
	}

	ch := make(chan *client.SolveStatus)
	done := make(chan struct{})

	display, err := progressui.NewDisplay(os.Stderr, progressui.PlainMode)
	if err != nil {
		return err
	}

	go func() {
		defer close(done)
		if _, err := display.UpdateFrom(ctx, ch); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return
			}
			slog.Error("progress display error", "error", err)
		}
	}()

	var solved bool
	_, err = bkc.Build(ctx, so, "", func(ctx context.Context, gwc gwclient.Client) (*gwclient.Result, error) {
		toolchain, err := baseImage(ctx, gwc)
		if err != nil {
			return nil, err
		}

		content, err := websiteContext(ctx, gwc)
		if err != nil {
			return nil, err
		}

		content = content.With(generateSite(toolchain))

		fsys, err := bkfs.EvalFromState(ctx, &content, gwc)
		if err != nil {
			return nil, err
		}

		solved = true

		l, err := net.Listen("tcp", addr.String())
		if err != nil {
			return nil, err
		}
		defer l.Close()

		handler := http.FileServerFS(fsys)
		srv := &http.Server{
			Handler: handler,
		}
		go srv.Serve(l) //nolint:errcheck

		url := "http://" + l.Addr().String()
		if err := browser.OpenURL(url); err != nil {
			slog.Warn("failed to open browser", "error", err)
		}
		slog.Info("Doc website started and available at addr", "url", url)
		<-ctx.Done()
		slog.Info("shutting down server", "reason", ctx.Err())
		srv.Shutdown(context.WithoutCancel(ctx)) //nolint:errcheck

		return gwclient.NewResult(), nil
	}, ch)

	if err == nil {
		return nil
	}

	if solved && errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	return err
}
