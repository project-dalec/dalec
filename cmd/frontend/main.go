package main

import (
	_ "embed"
	"flag"
	"fmt"
	"os"

	"github.com/moby/buildkit/frontend/gateway/grpcclient"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/moby/buildkit/util/bklog"
	"github.com/project-dalec/dalec/frontend"
	"github.com/project-dalec/dalec/internal/frontendapi"
	"github.com/project-dalec/dalec/internal/testrunner"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/grpclog"
)

const (
	Package = "github.com/project-dalec/dalec/cmd/frontend"

	credHelperSubcmd = "credential-helper"
)

func init() {
	bklog.L.Logger.SetOutput(os.Stderr)
	grpclog.SetLoggerV2(grpclog.NewLoggerV2WithVerbosity(bklog.L.WriterLevel(logrus.InfoLevel), bklog.L.WriterLevel(logrus.WarnLevel), bklog.L.WriterLevel(logrus.ErrorLevel), 1))
}

func main() {
	fs := flag.CommandLine
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `usage: %s [subcommand [args...]]`, os.Args[0])
	}

	if err := fs.Parse(os.Args); err != nil {
		bklog.L.WithError(err).Fatal("error parsing frontend args")
		os.Exit(70) // 70 is EX_SOFTWARE, meaning internal software error occurred

	}

	subCmd := fs.Arg(1)

	// NOTE: for subcommands we take args[2:]
	// skip args[0] (the executable) and args[1] (the subcommand)

	// each "sub-main" function handles its own exit
	switch subCmd {
	case credHelperSubcmd:
		args := flag.Args()[2:]
		gomodMain(args)
	case testrunner.StepRunnerCmdName:
		args := flag.Args()[2:]
		testrunner.StepCmd(args)
	case testrunner.CheckFilesCmdName:
		args := flag.Args()[2:]
		testrunner.CheckFilesCmd(args)
	case frontend.TestErrorCmdName:
		args := flag.Args()[2:]
		frontend.TestErrorCmd(args)
	default:
		dalecMain()
	}
}

func dalecMain() {
	ctx := appcontext.Context()
	mux, err := frontendapi.NewBuildRouter(ctx)
	if err != nil {
		bklog.L.WithError(err).Fatal("error creating frontend router")
	}

	if err := grpcclient.RunFromEnvironment(ctx, mux.Handler(frontend.WithTargetForwardingHandler)); err != nil {
		bklog.L.WithError(err).Fatal("error running frontend")
		os.Exit(70) // 70 is EX_SOFTWARE, meaning internal software error occurred
	}
}
