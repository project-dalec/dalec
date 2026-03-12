package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests/targets"
	"github.com/moby/buildkit/solver/pb"
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

	r, err := frontendapi.NewRouter(ctx)
	if err != nil {
		bklog.L.Fatal(err)
	}

	var client gwclient.Client = &targetPrintClient{}
	res, err := r.Handle(ctx, client)
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
		LLBCaps: pb.Caps.CapSet(pb.Caps.All()),
		Caps:    pb.Caps.CapSet(pb.Caps.All()),
	}
}

func (t *targetPrintClient) Inputs(_ context.Context) (map[string]llb.State, error) {
	return map[string]llb.State{
		"dockerfile": llb.Scratch(),
	}, nil
}

func (t *targetPrintClient) Solve(_ context.Context, _ gwclient.SolveRequest) (*gwclient.Result, error) {
	res := gwclient.NewResult()
	res.SetRef(&emptySpecRef{})
	return res, nil
}

// emptySpecRef implements gwclient.Reference and returns an empty YAML object
// from ReadFile, which LoadSpec interprets as an empty dalec.Spec.
type emptySpecRef struct {
	gwclient.Reference
}

func (r *emptySpecRef) ReadFile(_ context.Context, _ gwclient.ReadRequest) ([]byte, error) {
	return []byte("{}"), nil
}
