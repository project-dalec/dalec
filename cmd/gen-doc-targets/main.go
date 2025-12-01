package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests/targets"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/moby/buildkit/util/bklog"
	"github.com/project-dalec/dalec/internal/frontendapi"
	"github.com/sirupsen/logrus"
)

func main() {
	flag.Parse()

	bklog.L.Logger.SetOutput(os.Stderr)
	bklog.L.Logger.SetLevel(logrus.ErrorLevel)
	ctx := appcontext.Context()

	mux, err := frontendapi.NewBuildRouter(ctx)
	if err != nil {
		bklog.L.Fatal(err)
	}

	var client gwclient.Client = &targetPrintClient{}
	res, err := mux.Handle(ctx, client)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	dt, ok := res.Metadata["result.txt"]
	if !ok {
		fmt.Fprintln(os.Stderr, "no result.txt in response")
		os.Exit(70)
	}

	out := os.Stdout
	if nArgs := flag.NArg(); nArgs > 0 {
		if nArgs != 1 {
			fmt.Fprintln(os.Stderr, "only one argument (output file) is supported")
			os.Exit(1)
		}

		f, err := os.OpenFile(flag.Arg(0), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to open output file: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		out = f
	}

	_, err = io.Copy(out, bytes.NewReader(dt))
	if err != nil {
		fmt.Fprintln(os.Stderr, "failed to write output:", err)
		os.Exit(70)
	}
}

type targetPrintClient struct {
	gwclient.Client
}

func (t *targetPrintClient) BuildOpts() gwclient.BuildOpts {
	return gwclient.BuildOpts{
		Opts: map[string]string{
			"requestid": targets.SubrequestsTargetsDefinition.Name,
		},
	}
}
