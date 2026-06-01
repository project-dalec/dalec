package testenv

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/project-dalec/dalec/internal/frontendcoverage"
)

const covMetaFileHashOffset = 4 + 4 + 8 + 8

func gunzip(b []byte) (out []byte, retErr error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := zr.Close(); retErr == nil && err != nil {
			retErr = err
		}
	}()

	out, retErr = io.ReadAll(zr)
	return out, retErr
}

// Writes files compatible with `go tool covdata`:
//
//	covmeta.<hash>
//	covcounters.<hash>.<pid>.<ts>
func writeFrontendCovdata(outDir string, res *gwclient.Result, solveErr error) error {
	if outDir == "" {
		return nil
	}

	payload, err := frontendcoverage.PayloadFromSolve(res, solveErr)
	if err != nil {
		return err
	}
	if payload == nil {
		// Not every Solve necessarily runs the dalec frontend; only write when present.
		return nil
	}

	meta, err := gunzip(payload.MetaGz)
	if err != nil {
		return err
	}
	counters, err := gunzip(payload.CountersGz)
	if err != nil {
		return err
	}

	hash, err := covdataMetaHash(meta)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	// Write meta once (best-effort; safe under concurrency)
	metaPath := filepath.Join(outDir, "covmeta."+hash)
	if _, err := os.Stat(metaPath); os.IsNotExist(err) {
		tmp, err := os.CreateTemp(outDir, "covmeta."+hash+".tmp-*")
		if err != nil {
			return err
		}
		if _, err := tmp.Write(meta); err != nil {
			tmp.Close()
			_ = os.Remove(tmp.Name())
			return err
		}
		if err := tmp.Close(); err != nil {
			_ = os.Remove(tmp.Name())
			return err
		}
		// If rename fails because another goroutine already created it, ignore.
		if err := os.Rename(tmp.Name(), metaPath); err != nil {
			_ = os.Remove(tmp.Name())
		}
	}

	pid := os.Getpid()
	ts := time.Now().UnixNano()
	for {
		ctrPath := filepath.Join(outDir, fmt.Sprintf("covcounters.%s.%d.%d", hash, pid, ts))
		f, err := os.OpenFile(ctrPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if os.IsExist(err) {
			ts++
			continue
		}
		if err != nil {
			return err
		}
		if _, err := f.Write(counters); err != nil {
			_ = f.Close()
			_ = os.Remove(ctrPath)
			return err
		}
		if err := f.Close(); err != nil {
			_ = os.Remove(ctrPath)
			return err
		}
		return nil
	}
}

func covdataMetaHash(meta []byte) (string, error) {
	if len(meta) < covMetaFileHashOffset+16 {
		return "", fmt.Errorf("coverage metadata is too short: %d bytes", len(meta))
	}
	length := binary.LittleEndian.Uint64(meta[8:16])
	if int(length) != len(meta) {
		return "", fmt.Errorf("coverage metadata length mismatch: header=%d actual=%d", length, len(meta))
	}
	return hex.EncodeToString(meta[covMetaFileHashOffset : covMetaFileHashOffset+16]), nil
}
