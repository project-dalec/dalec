package testenv

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/project-dalec/dalec/internal/frontendcoverage"
)

func TestWriteFrontendCovdata(t *testing.T) {
	rawMeta := []byte("raw-meta")
	rawCounters := []byte("raw-counters")
	payload := &frontendcoverage.Payload{
		MetaGz:     gzipBytesForTest(t, rawMeta),
		CountersGz: gzipBytesForTest(t, rawCounters),
	}

	testCases := []struct {
		name  string
		setup func(t *testing.T) (*gwclient.Result, error)
	}{
		{
			name: "result metadata",
			setup: func(t *testing.T) (*gwclient.Result, error) {
				res := gwclient.NewResult()
				payload.AttachToResult(res)
				return res, nil
			},
		},
		{
			name: "grpc error detail",
			setup: func(t *testing.T) (*gwclient.Result, error) {
				errWithPayload, err := payload.AttachToError(errors.New("solve failed"))
				if err != nil {
					t.Fatalf("expected nil attach error, got %v", err)
				}
				return nil, errWithPayload
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			outDir := t.TempDir()
			res, err := tc.setup(t)

			if writeErr := writeFrontendCovdata(outDir, res, err); writeErr != nil {
				t.Fatalf("expected nil write error, got %v", writeErr)
			}

			hash := sha256.Sum256(rawMeta)
			hashHex := hex.EncodeToString(hash[:])

			metaPath := filepath.Join(outDir, "covmeta."+hashHex)
			gotMeta, readErr := os.ReadFile(metaPath)
			if readErr != nil {
				t.Fatalf("expected covmeta file, got %v", readErr)
			}
			if !bytes.Equal(gotMeta, rawMeta) {
				t.Fatalf("unexpected covmeta contents: %q", gotMeta)
			}

			counterFiles, globErr := filepath.Glob(filepath.Join(outDir, "covcounters."+hashHex+".*"))
			if globErr != nil {
				t.Fatalf("expected nil glob error, got %v", globErr)
			}
			if len(counterFiles) != 1 {
				t.Fatalf("expected exactly one counter file, got %d", len(counterFiles))
			}

			gotCounters, readErr := os.ReadFile(counterFiles[0])
			if readErr != nil {
				t.Fatalf("expected covcounters file, got %v", readErr)
			}
			if !bytes.Equal(gotCounters, rawCounters) {
				t.Fatalf("unexpected covcounters contents: %q", gotCounters)
			}
		})
	}
}

func gzipBytesForTest(t *testing.T, in []byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(in); err != nil {
		t.Fatalf("expected nil gzip write error, got %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("expected nil gzip close error, got %v", err)
	}

	return buf.Bytes()
}
