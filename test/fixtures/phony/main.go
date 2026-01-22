package main

import (
	"context"
	"os"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/gateway/grpcclient"
	"github.com/moby/buildkit/frontend/subrequests/targets"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/moby/buildkit/util/bklog"
	"github.com/project-dalec/dalec/frontend"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/grpclog"
)

func main() {
	bklog.L.Logger.SetOutput(os.Stderr)
	grpclog.SetLoggerV2(grpclog.NewLoggerV2WithVerbosity(bklog.L.WriterLevel(logrus.InfoLevel), bklog.L.WriterLevel(logrus.WarnLevel), bklog.L.WriterLevel(logrus.ErrorLevel), 1))

	ctx := appcontext.Context()

	var mux frontend.BuildMux

	mux.Add("check", phonyBuild, &targets.Target{
		Name:        "check",
		Description: "a phony target for a phony test world",
	})
	mux.Add("debug/resolve", phonyResolve, &targets.Target{
		Name:        "debug/resolve",
		Description: "a phony resolve target for testing namespaced targets",
	})

	if err := grpcclient.RunFromEnvironment(ctx, mux.Handle); err != nil {
		bklog.L.WithError(err).Fatal("error running frontend")
		os.Exit(70) // 70 is EX_SOFTWARE, meaning internal software error occurred
	}
}

func phonyBuild(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	def, err := llb.Scratch().File(llb.Mkfile("hello", 0o644, []byte("phony hello"))).Marshal(ctx)
	if err != nil {
		return nil, err
	}

	return client.Solve(ctx, gwclient.SolveRequest{
		Definition: def.ToPB(),
	})
}

func phonyResolve(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	def, err := llb.Scratch().File(llb.Mkfile("resolve", 0o644, []byte("phony resolve"))).Marshal(ctx)
	if err != nil {
		return nil, err
	}
	return client.Solve(ctx, gwclient.SolveRequest{
		Definition: def.ToPB(),
	})
}
