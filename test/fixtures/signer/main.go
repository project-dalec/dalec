package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/gateway/grpcclient"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/moby/buildkit/util/bklog"
	"github.com/pkg/errors"
	"github.com/project-dalec/dalec"
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
		replaceFile := bopts["build-arg:DALEC_TEST_SIGNER_REPLACE_FILE"]
		failFile := bopts["build-arg:DALEC_TEST_SIGNER_FAIL_FILE"]
		replacements := make(map[string][]byte)
		err = fs.WalkDir(artifactsFS, ".", func(p string, info fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if info.IsDir() {
				return nil
			}

			files = append(files, p)
			if p == failFile {
				return fmt.Errorf("test signer failed for %q", p)
			}
			if p == replaceFile || replaceFile == "*" {
				dt, err := fs.ReadFile(artifactsFS, p)
				if err != nil {
					return err
				}
				replacements[p] = append([]byte("signed:"), dt...)
			}
			return nil
		})
		if err != nil {
			return nil, errors.Wrap(err, "error walking artifacts")
		}

		mfst, err := json.Marshal(files)
		if err != nil {
			return nil, errors.Wrap(err, "error marshalling file manifest")
		}

		pg := dalec.ProgressGroup("phony signer output")
		output := llb.Scratch().
			File(llb.Mkfile("/target", 0o600, []byte(target)), pg).
			File(llb.Mkfile("/config.json", 0o600, configBytes), pg).
			File(llb.Mkfile("/manifest.json", 0o600, mfst), pg)
		for p, dt := range dalec.SortedMapIter(replacements) {
			if dir := path.Dir(p); dir != "." {
				output = output.File(llb.Mkdir("/"+dir, 0o755, llb.WithParents(true)), pg)
			}
			output = output.File(llb.Mkfile("/"+p, 0o600, dt), pg)
		}

		// For any build-arg seen, write a file to /env/<KEY> with the contents
		// being the value of the arg.
		for k, v := range dalec.SortedMapIter(c.BuildOpts().Opts) {
			_, key, ok := strings.Cut(k, "build-arg:")
			if !ok {
				// not a build arg
				continue
			}
			output = output.
				File(llb.Mkdir("/env", 0o755), pg).
				File(llb.Mkfile("/env/"+key, 0o600, []byte(v)), pg)
		}

		def, err := output.Marshal(ctx, pg)
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
