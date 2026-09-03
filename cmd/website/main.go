package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
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
)

const siteBasePath = "/dalec/"

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
	ap := netip.MustParseAddrPort("127.0.0.1:3000")
	fl := addrPortFlag(ap)
	return &fl
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	addrFl := newDefaultAddrPortFlag()
	flag.Var(addrFl, "addr", "<addr>:<port> to serve the docs site on")
	debugFl := flag.Bool("debug", false, "enable debug logging")
	sourceFl := flag.String("source", "website", "path to the Hugo website")

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

	if err := website(ctx, client, addrFl.ToAddrPort(), *sourceFl); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func baseImage(ctx context.Context, client gwclient.Client, site llb.State) (llb.State, error) {
	def, err := site.Marshal(ctx)
	if err != nil {
		return llb.Scratch(), err
	}
	res, err := client.Solve(ctx, gwclient.SolveRequest{
		Frontend: "dockerfile.v0",
		FrontendInputs: map[string]*pb.Definition{
			dockerui.DefaultLocalNameContext:    def.ToPB(),
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

	return *bctx, nil
}

// generateSite returns a state option that generates the static website content
// using the provided toolchain.
// The input state of the state option is the content to generate the site from.
func generateSite(toolchain llb.State) llb.StateOption {
	return func(in llb.State) llb.State {
		const (
			modsCacheID = "dalec-website-go-mod"
			hugoCacheID = "dalec-website-hugo"
		)
		cacheMounts := dalec.RunOptFunc(func(ei *llb.ExecInfo) {
			goModPersistentCacheID := dalec.PersistentCacheID{Type: modsCacheID}.String()
			hugoPersistentCacheID := dalec.PersistentCacheID{Type: hugoCacheID}.String()
			llb.AddMount(
				"/go/pkg/mod",
				llb.Scratch(),
				llb.AsPersistentCacheDir(goModPersistentCacheID, llb.CacheMountLocked),
			).SetRunOption(ei)
			llb.AddMount(
				"/cache",
				llb.Scratch(),
				llb.AsPersistentCacheDir(hugoPersistentCacheID, llb.CacheMountLocked),
			).SetRunOption(ei)
		})
		generated := toolchain.Run(
			cacheMounts,
			llb.Dir("/project"),
			llb.AddEnv("GOPATH", "/go"),
			llb.AddEnv("HUGO_CACHEDIR", "/cache"),
			llb.AddEnv("PATH", "/usr/local/go/bin:/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"),
			llb.Args([]string{"hugo", "--gc", "--minify", "--baseURL", siteBasePath}),
			llb.WithCustomName("Build Hugo website"),
		).AddMount("/project", in)

		out := llb.Scratch().File(llb.Copy(generated, "public", "/", dalec.WithDirContentsOnly()), llb.WithCustomName("Get static content"))

		return out
	}
}

func website(ctx context.Context, bkc *client.Client, addr netip.AddrPort, source string) error {
	defer bkc.Close() //nolint:errcheck

	siteFS, err := fsutil.NewFS(source)
	if err != nil {
		return fmt.Errorf("load website source: %w", err)
	}

	so := client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameContext: siteFS,
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
		content, err := websiteContext(ctx, gwc)
		if err != nil {
			return nil, err
		}

		toolchain, err := baseImage(ctx, gwc, content)
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

		srv := &http.Server{
			Handler: newSiteHandler(fsys),
		}
		go srv.Serve(l) //nolint:errcheck

		url := "http://" + l.Addr().String() + siteBasePath
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

	if solved && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return nil
	}
	return err
}

func newSiteHandler(site fs.FS) http.Handler {
	fileServer := http.FileServerFS(site)
	mux := http.NewServeMux()
	mux.Handle(siteBasePath, http.StripPrefix(strings.TrimSuffix(siteBasePath, "/"), fileServer))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, siteBasePath, http.StatusTemporaryRedirect)
	})
	return mux
}
