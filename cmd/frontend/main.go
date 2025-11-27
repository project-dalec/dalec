package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/containerd/plugin"
	"github.com/moby/buildkit/frontend/gateway/grpcclient"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/moby/buildkit/util/bklog"
	"github.com/project-dalec/dalec/frontend"
	"github.com/project-dalec/dalec/frontend/debug"
	"github.com/project-dalec/dalec/internal/plugins"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/grpclog"

	_ "github.com/project-dalec/dalec/internal/commands"
)

const (
	Package = "github.com/project-dalec/dalec/cmd/frontend"
)

func init() {
	bklog.L.Logger.SetOutput(os.Stderr)
	grpclog.SetLoggerV2(grpclog.NewLoggerV2WithVerbosity(bklog.L.WriterLevel(logrus.InfoLevel), bklog.L.WriterLevel(logrus.WarnLevel), bklog.L.WriterLevel(logrus.ErrorLevel), 1))
}

func main() {
	flags := flag.NewFlagSet(filepath.Base(os.Args[0]), flag.ExitOnError)

	if err := flags.Parse(os.Args[1:]); err != nil {
		bklog.L.WithError(err).Fatal("error parsing frontend args")
		os.Exit(70) // 70 is EX_SOFTWARE, meaning internal software error occurred
	}

	ctx := appcontext.Context()

	if flags.NArg() == 0 {
		dalecMain(ctx)
		return
	}

	h, err := lookupCmd(ctx, flags.Arg(0))
	if err != nil {
		bklog.L.WithError(err).Fatal("error handling command")
		os.Exit(70) // 70 is EX_SOFTWARE, meaning internal software error occurred
	}

	if h == nil {
		fmt.Fprintln(os.Stderr, "unknown subcommand:", flags.Arg(1))
		fmt.Fprintln(os.Stderr, "full args:", flag.Args())
		fmt.Fprintln(os.Stderr, "If you see this message this is probably a bug in dalec.")
		os.Exit(64) // 64 is EX_USAGE, meaning command line usage error
	}

	h.HandleCmd(ctx, flags.Args()[1:])
}

func lookupCmd(ctx context.Context, cmd string) (plugins.CmdHandler, error) {
	set := plugin.NewPluginSet()
	filter := func(r *plugins.Registration) bool {
		return r.Type != plugins.TypeCmd || r.ID != cmd
	}

	for _, r := range plugins.Graph(filter) {
		cfg := plugin.NewContext(ctx, set, nil)

		p := r.Init(cfg)

		v, err := p.Instance()
		if err != nil && !plugin.IsSkipPlugin(err) {
			return nil, err
		}

		return v.(plugins.CmdHandler), nil
	}

	return nil, nil
}

func dalecMain(ctx context.Context) {
	var mux frontend.BuildMux
	mux.Add(debug.DebugRoute, debug.Handle, nil)

	if err := loadPlugins(ctx, &mux); err != nil {
		bklog.L.WithError(err).Fatal("error loading plugins")
		os.Exit(1)
	}

	if err := grpcclient.RunFromEnvironment(ctx, mux.Handler(frontend.WithTargetForwardingHandler)); err != nil {
		bklog.L.WithError(err).Fatal("error running frontend")
		os.Exit(70) // 70 is EX_SOFTWARE, meaning internal software error occurred
	}
}
