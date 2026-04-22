package frontendcoverage

import (
	"bytes"
	"encoding/base64"
	"strings"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/status"
)

const (
	OptKey      = "dalec.coverage"
	MetaKey     = "dalec.coverage.frontend.meta.gz"
	CountersKey = "dalec.coverage.frontend.counters.gz"

	errorInfoReason = "DALEC_FRONTEND_COVERAGE"
	errorInfoDomain = "github.com/project-dalec/dalec"

	errorInfoMetaKey     = "frontend_meta_gz"
	errorInfoCountersKey = "frontend_counters_gz"
)

type Payload struct {
	MetaGz     []byte
	CountersGz []byte
}

func Want(opts map[string]string) bool {
	v, ok := opts[OptKey]
	if !ok {
		return false
	}

	v = strings.ToLower(strings.TrimSpace(v))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func (p *Payload) empty() bool {
	return p == nil || len(p.MetaGz) == 0 || len(p.CountersGz) == 0
}

func (p *Payload) AttachToResult(res *gwclient.Result) {
	if p.empty() || res == nil {
		return
	}
	if res.Metadata == nil {
		res.Metadata = map[string][]byte{}
	}

	res.Metadata[MetaKey] = bytes.Clone(p.MetaGz)
	res.Metadata[CountersKey] = bytes.Clone(p.CountersGz)
}

func PayloadFromResult(res *gwclient.Result) *Payload {
	if res == nil || res.Metadata == nil {
		return nil
	}

	meta := res.Metadata[MetaKey]
	counters := res.Metadata[CountersKey]
	if len(meta) == 0 || len(counters) == 0 {
		return nil
	}

	return &Payload{
		MetaGz:     bytes.Clone(meta),
		CountersGz: bytes.Clone(counters),
	}
}

func (p *Payload) AttachToError(err error) (error, error) {
	if err == nil || p.empty() {
		return err, nil
	}

	st, attachErr := status.Convert(err).WithDetails(&errdetails.ErrorInfo{
		Reason: errorInfoReason,
		Domain: errorInfoDomain,
		Metadata: map[string]string{
			errorInfoMetaKey:     base64.StdEncoding.EncodeToString(p.MetaGz),
			errorInfoCountersKey: base64.StdEncoding.EncodeToString(p.CountersGz),
		},
	})
	if attachErr != nil {
		return err, attachErr
	}

	return &errorWithStatusDetail{err: err, st: st}, nil
}

func PayloadFromError(err error) (*Payload, error) {
	if err == nil {
		return nil, nil
	}

	st, ok := status.FromError(err)
	if !ok {
		return nil, nil
	}

	for _, detail := range st.Details() {
		info, ok := detail.(*errdetails.ErrorInfo)
		if !ok || info.Reason != errorInfoReason || info.Domain != errorInfoDomain {
			continue
		}

		meta, err := base64.StdEncoding.DecodeString(info.Metadata[errorInfoMetaKey])
		if err != nil {
			return nil, err
		}

		counters, err := base64.StdEncoding.DecodeString(info.Metadata[errorInfoCountersKey])
		if err != nil {
			return nil, err
		}

		if len(meta) == 0 || len(counters) == 0 {
			return nil, nil
		}

		return &Payload{
			MetaGz:     meta,
			CountersGz: counters,
		}, nil
	}

	return nil, nil
}

func PayloadFromSolve(res *gwclient.Result, err error) (*Payload, error) {
	if payload := PayloadFromResult(res); payload != nil {
		return payload, nil
	}

	return PayloadFromError(err)
}

type errorWithStatusDetail struct {
	err error
	st  *status.Status
}

func (e *errorWithStatusDetail) Error() string {
	return e.err.Error()
}

func (e *errorWithStatusDetail) Unwrap() error {
	return e.err
}

func (e *errorWithStatusDetail) GRPCStatus() *status.Status {
	return e.st
}
