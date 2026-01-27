package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/gateway/grpcclient"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/moby/buildkit/util/bklog"
	"github.com/pkg/errors"
	"github.com/project-dalec/dalec/frontend/pkg/bkfs"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/grpclog"
)

func main() {
	bklog.L.Logger.SetOutput(os.Stderr)
	grpclog.SetLoggerV2(grpclog.NewLoggerV2WithVerbosity(bklog.L.WriterLevel(logrus.InfoLevel), bklog.L.WriterLevel(logrus.WarnLevel), bklog.L.WriterLevel(logrus.ErrorLevel), 1))

	ctx := appcontext.Context()

	if err := grpcclient.RunFromEnvironment(ctx, func(ctx context.Context, c gwclient.Client) (*gwclient.Result, error) {
		bopts := c.BuildOpts().Opts
		target := bopts["dalec.target"]

		type config struct {
			OS string
		}

		cfg := config{}

		switch target {
		case "windowscross", "windows":
			cfg.OS = "windows"
		default:
			cfg.OS = "linux"
		}

		dc, err := dockerui.NewClient(c)
		if err != nil {
			return nil, err
		}

		bctx, err := dc.MainContext(ctx)
		if err != nil {
			return nil, err
		}

		if bctx == nil {
			return nil, fmt.Errorf("no artifact state provided to signer")
		}

		artifactsFS, err := bkfs.FromState(ctx, bctx, c)
		if err != nil {
			return nil, err
		}

		configBytes, err := json.Marshal(&cfg)
		if err != nil {
			return nil, err
		}

		var files []string
		err = fs.WalkDir(artifactsFS, ".", func(p string, info fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if info.IsDir() {
				return nil
			}

			files = append(files, p)
			return nil
		})
		if err != nil {
			return nil, errors.Wrap(err, "error walking artifacts")
		}

		mfst, err := json.Marshal(files)
		if err != nil {
			return nil, errors.Wrap(err, "error marshalling file manifest")
		}

		output := llb.Scratch().
			File(llb.Mkfile("/target", 0o600, []byte(target))).
			File(llb.Mkfile("/config.json", 0o600, configBytes)).
			File(llb.Mkfile("/manifest.json", 0o600, mfst))

		// For any build-arg seen, write a file to /env/<KEY> with the contents
		// being the value of the arg.
		for k, v := range c.BuildOpts().Opts {
			_, key, ok := strings.Cut(k, "build-arg:")
			if !ok {
				// not a build arg
				continue
			}
			output = output.
				File(llb.Mkdir("/env", 0o755)).
				File(llb.Mkfile("/env/"+key, 0o600, []byte(v)))
		}

		def, err := output.Marshal(ctx)
		if err != nil {
			return nil, err
		}

		return c.Solve(ctx, gwclient.SolveRequest{
			Definition: def.ToPB(),
		})
	}); err != nil {
		bklog.L.WithError(err).Fatal("error running frontend")
		os.Exit(70) // 70 is EX_SOFTWARE, meaning internal software error occurred
	}
}
