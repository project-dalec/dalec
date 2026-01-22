// cmd/frontend/coverage.go
package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"strings"

	"runtime/coverage"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

const (
	frontendCoverageOptKey = "dalec.coverage"
	frontendCovMetaKey     = "dalec.coverage.frontend.meta.gz"
	frontendCovCountersKey = "dalec.coverage.frontend.counters.gz"
)

func isNoMetaErr(err error) bool {
	if err == nil {
		return false
	}
	// runtime/coverage: "no meta-data available (binary not built with -cover?)"
	return strings.Contains(strings.ToLower(err.Error()), "no meta-data available")
}

// Enabled per solve via SolveRequest.FrontendOpt["dalec.coverage"]="1"
func wantFrontendCoverage(c gwclient.Client) bool {
	v, ok := c.BuildOpts().Opts[frontendCoverageOptKey]
	if !ok {
		return false
	}
	v = strings.ToLower(strings.TrimSpace(v))
	return v == "1" || v == "true" || v == "yes" || v == "on"
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

func attachFrontendCoverage(c gwclient.Client, res *gwclient.Result) error {
	if res == nil || !wantFrontendCoverage(c) {
		return nil
	}
	if res.Metadata == nil {
		res.Metadata = map[string][]byte{}
	}

	var metaBuf, ctrBuf bytes.Buffer

	if err := coverage.WriteMeta(&metaBuf); err != nil {
		if isNoMetaErr(err) {
			return nil
		}
		return err
	}
	if err := coverage.WriteCounters(&ctrBuf); err != nil {
		if isNoMetaErr(err) {
			return nil
		}
		return err
	}

	metaGz, err := gzipBytes(metaBuf.Bytes())
	if err != nil {
		return err
	}
	ctrGz, err := gzipBytes(ctrBuf.Bytes())
	if err != nil {
		return err
	}

	res.Metadata[frontendCovMetaKey] = metaGz
	res.Metadata[frontendCovCountersKey] = ctrGz

	// Avoid cross-solve accumulation if the frontend process is reused.
	// Only works for binaries built with -cover (and typically atomic counters).
	_ = coverage.ClearCounters()

	return nil
}

func wrapWithCoverage(next gwclient.BuildFunc) gwclient.BuildFunc {
	return func(ctx context.Context, c gwclient.Client) (*gwclient.Result, error) {
		res, err := next(ctx, c)
		if err != nil {
			return nil, err
		}
		if err := attachFrontendCoverage(c, res); err != nil {
			return nil, err
		}
		return res, nil
	}
}
