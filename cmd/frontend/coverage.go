package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"strings"

	"runtime/coverage"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/project-dalec/dalec/internal/frontendcoverage"
)

func isNoMetaErr(err error) bool {
	if err == nil {
		return false
	}
	// runtime/coverage: "no meta-data available (binary not built with -cover?)"
	return strings.Contains(strings.ToLower(err.Error()), "no meta-data available")
}

// Enabled per solve via SolveRequest.FrontendOpt[frontendcoverage.OptKey]="1"
func wantFrontendCoverage(c gwclient.Client) bool {
	return frontendcoverage.Want(c.BuildOpts().Opts)
}

func gzipBytes(in []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(in); err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

var frontendCoverageCollector = collectFrontendCoveragePayload

func collectFrontendCoveragePayload() (*frontendcoverage.Payload, error) {
	var metaBuf, ctrBuf bytes.Buffer

	if err := coverage.WriteMeta(&metaBuf); err != nil {
		if isNoMetaErr(err) {
			return nil, nil
		}
		return nil, err
	}
	if err := coverage.WriteCounters(&ctrBuf); err != nil {
		if isNoMetaErr(err) {
			return nil, nil
		}
		return nil, err
	}

	metaGz, err := gzipBytes(metaBuf.Bytes())
	if err != nil {
		return nil, err
	}
	ctrGz, err := gzipBytes(ctrBuf.Bytes())
	if err != nil {
		return nil, err
	}

	// Avoid cross-solve accumulation if the frontend process is reused.
	// Only works for binaries built with -cover (and typically atomic counters).
	_ = coverage.ClearCounters()

	return &frontendcoverage.Payload{
		MetaGz:     metaGz,
		CountersGz: ctrGz,
	}, nil
}

func wrapWithCoverage(next gwclient.BuildFunc) gwclient.BuildFunc {
	return func(ctx context.Context, c gwclient.Client) (*gwclient.Result, error) {
		res, err := next(ctx, c)
		if !wantFrontendCoverage(c) {
			return res, err
		}

		payload, covErr := frontendCoverageCollector()
		if covErr != nil {
			if err != nil {
				return res, errors.Join(err, covErr)
			}
			return res, covErr
		}
		if payload == nil {
			return res, err
		}

		if err != nil {
			errWithCoverage, attachErr := payload.AttachToError(err)
			if attachErr != nil {
				return res, errors.Join(err, attachErr)
			}
			return res, errWithCoverage
		}

		if res == nil {
			res = gwclient.NewResult()
		}
		payload.AttachToResult(res)

		return res, nil
	}
}
