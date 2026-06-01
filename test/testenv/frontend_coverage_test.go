package testenv

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/project-dalec/dalec/internal/frontendcoverage"
)

func TestWriteFrontendCovdata(t *testing.T) {
	metaHash := [16]byte{0x01, 0x02, 0x03, 0x04, 0x05}
	rawMeta := covMetaForTest(t, metaHash)
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

			hashHex := hex.EncodeToString(metaHash[:])

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
			assertCounterFileName(t, hashHex, counterFiles[0])

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

func TestGoToolCovdataRequiresStandardCounterFileName(t *testing.T) {
	moduleDir := t.TempDir()
	writeFileForTest(t, filepath.Join(moduleDir, "go.mod"), []byte("module example.com/covsample\n\ngo 1.25.0\n"))
	writeFileForTest(t, filepath.Join(moduleDir, "main.go"), []byte(`package main

import "fmt"

func main() {
	if 1+1 == 2 {
		fmt.Println("covered")
	}
}
`))

	exeName := "covsample"
	if runtime.GOOS == "windows" {
		exeName += ".exe"
	}
	exePath := filepath.Join(moduleDir, exeName)
	runCmdInDir(t, moduleDir, "go", "build", "-cover", "-o", exePath, ".")

	goodDir := filepath.Join(t.TempDir(), "good")
	if err := os.MkdirAll(goodDir, 0o755); err != nil {
		t.Fatalf("expected nil mkdir error, got %v", err)
	}
	runCoveredBinary(t, exePath, goodDir)

	metaPath := mustSingleMatch(t, filepath.Join(goodDir, "covmeta.*"))
	counterPath := mustSingleMatch(t, filepath.Join(goodDir, "covcounters.*"))

	goodProfile := filepath.Join(t.TempDir(), "good.out")
	runCovdataTextfmt(t, goodDir, goodProfile)
	assertCoverageProfileHasNonZeroBlock(t, goodProfile)

	badDir := filepath.Join(t.TempDir(), "bad")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatalf("expected nil mkdir error, got %v", err)
	}
	copyFileForTest(t, metaPath, filepath.Join(badDir, filepath.Base(metaPath)))
	copyFileForTest(t, counterPath, filepath.Join(badDir, filepath.Base(counterPath)+".extra"))

	badProfile := filepath.Join(t.TempDir(), "bad.out")
	runCovdataTextfmt(t, badDir, badProfile)
	assertCoverageProfileHasOnlyZeroBlocks(t, badProfile)
}

func assertCounterFileName(t *testing.T, hash, path string) {
	t.Helper()

	base := filepath.Base(path)
	parts := strings.Split(base, ".")
	if len(parts) != 4 {
		t.Fatalf("expected counter filename %q to have 4 dot-separated parts, got %d", base, len(parts))
	}
	if parts[0] != "covcounters" {
		t.Fatalf("expected counter filename prefix covcounters, got %q", parts[0])
	}
	if parts[1] != hash {
		t.Fatalf("expected counter filename hash %q, got %q", hash, parts[1])
	}
	if _, err := strconv.Atoi(parts[2]); err != nil {
		t.Fatalf("expected numeric pid in counter filename %q, got %q: %v", base, parts[2], err)
	}
	if _, err := strconv.ParseInt(parts[3], 10, 64); err != nil {
		t.Fatalf("expected numeric timestamp in counter filename %q, got %q: %v", base, parts[3], err)
	}
}

func runCmdInDir(t *testing.T, dir, name string, args ...string) {
	t.Helper()

	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected command %q %v to succeed, got %v\n%s", name, args, err, out)
	}
}

func runCoveredBinary(t *testing.T, exePath, covDir string) {
	t.Helper()

	cmd := exec.Command(exePath)
	cmd.Env = append(os.Environ(), "GOCOVERDIR="+covDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected covered binary to succeed, got %v\n%s", err, out)
	}
}

func runCovdataTextfmt(t *testing.T, covDir, outPath string) {
	t.Helper()

	cmd := exec.Command("go", "tool", "covdata", "textfmt", "-i="+covDir, "-o="+outPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected covdata textfmt to succeed, got %v\n%s", err, out)
	}
}

func mustSingleMatch(t *testing.T, pattern string) string {
	t.Helper()

	matches, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("expected nil glob error for %q, got %v", pattern, err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly one match for %q, got %d", pattern, len(matches))
	}
	return matches[0]
}

func copyFileForTest(t *testing.T, src, dst string) {
	t.Helper()

	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("expected read of %q to succeed, got %v", src, err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatalf("expected write of %q to succeed, got %v", dst, err)
	}
}

func assertCoverageProfileHasNonZeroBlock(t *testing.T, path string) {
	t.Helper()

	sawBlock, sawNonZero, profile := coverageProfileStats(t, path)
	if !sawBlock {
		t.Fatalf("expected coverage profile %q to contain blocks, got:\n%s", path, profile)
	}
	if !sawNonZero {
		t.Fatalf("expected coverage profile %q to contain a non-zero block, got:\n%s", path, profile)
	}
}

func assertCoverageProfileHasOnlyZeroBlocks(t *testing.T, path string) {
	t.Helper()

	sawBlock, sawNonZero, profile := coverageProfileStats(t, path)
	if !sawBlock {
		t.Fatalf("expected coverage profile %q to contain blocks, got:\n%s", path, profile)
	}
	if sawNonZero {
		t.Fatalf("expected coverage profile %q to contain only zero-count blocks, got:\n%s", path, profile)
	}
}

func coverageProfileStats(t *testing.T, path string) (bool, bool, string) {
	t.Helper()

	profile, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected read of %q to succeed, got %v", path, err)
	}

	sawBlock := false
	sawNonZero := false
	for _, line := range strings.Split(strings.TrimSpace(string(profile)), "\n") {
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}

		sawBlock = true
		fields := strings.Fields(line)
		if len(fields) != 3 {
			t.Fatalf("expected coverage line to have 3 fields, got %q", line)
		}

		count, err := strconv.ParseUint(fields[2], 10, 64)
		if err != nil {
			t.Fatalf("expected numeric coverage count in %q, got %v", line, err)
		}
		if count > 0 {
			sawNonZero = true
		}
	}

	return sawBlock, sawNonZero, string(profile)
}

func writeFileForTest(t *testing.T, path string, data []byte) {
	t.Helper()

	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("expected write of %q to succeed, got %v", path, err)
	}
}

func covMetaForTest(t *testing.T, hash [16]byte) []byte {
	t.Helper()

	meta := make([]byte, covMetaFileHashOffset+len(hash))
	binary.LittleEndian.PutUint64(meta[8:16], uint64(len(meta)))
	copy(meta[covMetaFileHashOffset:], hash[:])
	return meta
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
