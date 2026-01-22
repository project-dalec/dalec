package testenv

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

const (
	frontendCovMetaKey     = "dalec.coverage.frontend.meta.gz"
	frontendCovCountersKey = "dalec.coverage.frontend.counters.gz"
)

func gunzip(b []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}

// Writes files compatible with `go tool covdata`:
//   covmeta.<hash>
//   covcounters.<hash>.<pid>.<ts>.<rand>
func writeFrontendCovdata(outDir string, res *gwclient.Result) error {
	if outDir == "" || res == nil || res.Metadata == nil {
		return nil
	}

	metaGz := res.Metadata[frontendCovMetaKey]
	ctrGz := res.Metadata[frontendCovCountersKey]
	if metaGz == nil || ctrGz == nil {
		// Not every Solve necessarily runs the dalec frontend; only write when present.
		return nil
	}

	meta, err := gunzip(metaGz)
	if err != nil {
		return err
	}
	counters, err := gunzip(ctrGz)
	if err != nil {
		return err
	}

	sum := sha256.Sum256(meta)
	hash := hex.EncodeToString(sum[:])

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

	var rb [4]byte
	_, _ = rand.Read(rb[:])

	pid := os.Getpid()
	ts := time.Now().UnixNano()
	ctrPath := filepath.Join(outDir, fmt.Sprintf("covcounters.%s.%d.%d.%x", hash, pid, ts, rb))
	return os.WriteFile(ctrPath, counters, 0o644)
}
