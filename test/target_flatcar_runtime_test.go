package test

import (
	"bytes"
	"compress/bzip2"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/targets/linux/flatcar"
)

const (
	flatcarRuntimeVersion   = "4593.2.1"
	flatcarRuntimeBaseURL   = "https://stable.release.flatcar-linux.net/amd64-usr/" + flatcarRuntimeVersion
	flatcarRuntimeImageSHA  = "5a67c3cd363ac305ab3fb559ee8c6e3688a2f91308bf2370eb352a42ea718a41dd9b6c5607cac845ec698764ec231919c45865b4f15feefb08cd5ac32d8d5725"
	flatcarRuntimeScriptSHA = "6398d3089f2a214c3604ebc796fa3135763142a9969fcac9a8fd2a6f3b64ad48025201ec7b96f733f275e164ff9bc447a6e23caa5b53ceb565aaa4829ec9232e"
	flatcarRuntimeName      = "flatcar-runtime-test"
	flatcarRuntimeOutput    = "hello from a Dalec Flatcar sysext"
)

func TestFlatcarRuntime(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Flatcar runtime validation requires Linux")
	}
	requireFlatcarRuntimeCommand(t, "qemu-system-x86_64")
	requireFlatcarRuntimeCommand(t, "ssh")
	requireFlatcarRuntimeCommand(t, "ssh-keygen")

	ctx := startTestSpan(baseCtx, t)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	workDir := t.TempDir()
	outputDir := filepath.Join(workDir, "output")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatal(err)
	}

	sysext := buildFlatcarRuntimeSysext(ctx, t)
	sysextPath := filepath.Join(outputDir, flatcarRuntimeName+".raw")
	if err := os.WriteFile(sysextPath, sysext, 0o644); err != nil {
		t.Fatalf("error writing Flatcar sysext: %v", err)
	}

	cacheDir := flatcarRuntimeCacheDir(t)
	qemuImageBZ2 := filepath.Join(cacheDir, "flatcar_production_qemu_image.img.bz2")
	qemuScript := filepath.Join(cacheDir, "flatcar_production_qemu.sh")

	downloadVerified(ctx, t, flatcarRuntimeBaseURL+"/flatcar_production_qemu_image.img.bz2", qemuImageBZ2, flatcarRuntimeImageSHA)
	downloadVerified(ctx, t, flatcarRuntimeBaseURL+"/flatcar_production_qemu.sh", qemuScript, flatcarRuntimeScriptSHA)

	vmDir := filepath.Join(workDir, "vm")
	if err := os.MkdirAll(vmDir, 0o755); err != nil {
		t.Fatal(err)
	}
	imagePath := filepath.Join(vmDir, "flatcar_production_qemu_image.img")
	decompressBzip2File(t, qemuImageBZ2, imagePath)

	vmScript := filepath.Join(vmDir, "flatcar_production_qemu.sh")
	copyFile(t, qemuScript, vmScript, 0o755)

	keyPath := filepath.Join(workDir, "id_ed25519")
	runCommand(ctx, t, "", "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", keyPath)
	pubKey, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		t.Fatalf("error reading generated SSH public key: %v", err)
	}

	httpPort, shutdownHTTP := startFlatcarArtifactServer(t, outputDir)
	defer shutdownHTTP()

	sshPort := freeTCPPort(t)
	t.Logf("using Flatcar runtime ports: http=%d ssh=%d", httpPort, sshPort)

	sysextSum := sha512.Sum512(sysext)
	ignitionPath := filepath.Join(workDir, "config.ign")
	writeFlatcarIgnition(t, ignitionPath, string(bytes.TrimSpace(pubKey)), httpPort, hex.EncodeToString(sysextSum[:]))

	qemuLog := filepath.Join(workDir, "qemu.log")
	waitQEMU, qemuExited := startFlatcarQEMU(ctx, t, vmDir, ignitionPath, qemuLog, sshPort)

	waitForFlatcarSSH(ctx, t, keyPath, sshPort, qemuExited, qemuLog)

	remoteCommand := fmt.Sprintf(`set -eux
grep -q '^ID=flatcar$' /etc/os-release
sudo systemctl restart systemd-sysext
sudo systemd-sysext status --no-pager | grep -F %s
test -x %s
test "$(%s)" = %s
`, shellQuote(flatcarRuntimeName), shellQuote("/usr/bin/"+flatcarRuntimeName), shellQuote("/usr/bin/"+flatcarRuntimeName), shellQuote(flatcarRuntimeOutput))

	if out, err := runFlatcarSSH(ctx, keyPath, sshPort, remoteCommand); err != nil {
		t.Fatalf("Flatcar runtime validation failed: %v\nSSH output:\n%s\nQEMU output:\n%s", err, out, tailFile(qemuLog, 64*1024))
	}

	t.Logf("Flatcar loaded %s.raw and executed its binary successfully", flatcarRuntimeName)

	if out, err := runFlatcarSSH(ctx, keyPath, sshPort, "sudo systemctl poweroff"); err != nil {
		t.Logf("error powering off Flatcar VM: %v\n%s", err, out)
	}
	if err := waitQEMU(); err != nil {
		t.Logf("Flatcar QEMU exited after poweroff: %v", err)
	}
}

func buildFlatcarRuntimeSysext(ctx context.Context, t *testing.T) []byte {
	t.Helper()

	var sysext []byte
	spec := flatcarRuntimeSpec()
	platform := ocispecs.Platform{OS: "linux", Architecture: "amd64"}

	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
		res := solveT(ctx, t, gwc, newSolveRequest(
			withSpec(ctx, t, spec),
			withBuildTarget(flatcar.TargetKey+"/testing/sysext"),
			withPlatform(platform),
		))
		sysext = readFile(ctx, t, "/"+flatcarRuntimeName+".raw", res)
	})

	if len(sysext) == 0 {
		t.Fatalf("Flatcar sysext output is empty")
	}
	return sysext
}

func flatcarRuntimeSpec() *dalec.Spec {
	return &dalec.Spec{
		Name:        flatcarRuntimeName,
		Version:     "0.0.1",
		Revision:    "1",
		Packager:    "Dalec",
		Vendor:      "Dalec",
		License:     "Apache-2.0",
		Website:     "https://github.com/project-dalec/dalec",
		Description: "A fixture for validating Dalec sysext images on Flatcar.",
		Sources: map[string]dalec.Source{
			flatcarRuntimeName: {
				Inline: &dalec.SourceInline{
					File: &dalec.SourceInlineFile{
						Permissions: 0o755,
						Contents:    "#!/bin/sh\necho " + strconv.Quote(flatcarRuntimeOutput) + "\n",
					},
				},
			},
		},
		Artifacts: dalec.Artifacts{
			Binaries: map[string]dalec.ArtifactConfig{
				flatcarRuntimeName: {},
			},
		},
	}
}

func requireFlatcarRuntimeCommand(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err == nil {
		return
	}
	msg := fmt.Sprintf("Flatcar runtime validation requires %s", name)
	if os.Getenv("CI") == "true" {
		t.Fatal(msg)
	}
	t.Skip(msg)
}

func flatcarRuntimeCacheDir(t *testing.T) string {
	t.Helper()

	if dir := os.Getenv("FLATCAR_CACHE_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	cacheRoot := os.Getenv("XDG_CACHE_HOME")
	if cacheRoot == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatalf("error getting user home dir: %v", err)
		}
		cacheRoot = filepath.Join(home, ".cache")
	}

	dir := filepath.Join(cacheRoot, "dalec", "flatcar", flatcarRuntimeVersion)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func downloadVerified(ctx context.Context, t *testing.T, url, output, sha512Hex string) {
	t.Helper()

	if got, err := sha512File(output); err == nil && got == sha512Hex {
		t.Logf("using cached %s", output)
		return
	}

	if err := os.Remove(output); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error removing stale download %s: %v", output, err)
	}
	if err := downloadFile(ctx, url, output); err != nil {
		t.Fatalf("error downloading %s: %v", url, err)
	}

	got, err := sha512File(output)
	if err != nil {
		t.Fatalf("error hashing %s: %v", output, err)
	}
	if got != sha512Hex {
		t.Fatalf("unexpected sha512 for %s\n got: %s\nwant: %s", output, got, sha512Hex)
	}
}

func downloadFile(ctx context.Context, url, output string) error {
	tmp := output + ".tmp"
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	client := &http.Client{Timeout: 5 * time.Minute}
	var lastErr error
	for attempt := 1; attempt <= 5; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}

		resp, err := client.Do(req)
		if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			err = writeResponseBody(tmp, resp.Body)
			if err == nil {
				return os.Rename(tmp, output)
			}
			resp = nil
		}
		if resp != nil {
			if err == nil {
				err = fmt.Errorf("unexpected HTTP status %s", resp.Status)
			}
			if _, copyErr := io.Copy(io.Discard, resp.Body); copyErr != nil && err == nil {
				err = copyErr
			}
			if closeErr := resp.Body.Close(); closeErr != nil && err == nil {
				err = closeErr
			}
		}
		lastErr = err

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return lastErr
}

func writeResponseBody(path string, body io.ReadCloser) error {
	defer body.Close()
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, body)
	return err
}

func sha512File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha512.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func decompressBzip2File(t *testing.T, src, dest string) {
	t.Helper()

	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()

	out, err := os.Create(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()

	if _, err := io.Copy(out, bzip2.NewReader(in)); err != nil {
		t.Fatalf("error decompressing %s: %v", src, err)
	}
}

func copyFile(t *testing.T, src, dest string, mode os.FileMode) {
	t.Helper()

	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()

	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dest, mode); err != nil {
		t.Fatal(err)
	}
}

func writeFlatcarIgnition(t *testing.T, path, sshKey string, httpPort int, sysextHash string) {
	t.Helper()

	ignition := map[string]any{
		"ignition": map[string]any{
			"version": "3.3.0",
		},
		"passwd": map[string]any{
			"users": []map[string]any{
				{
					"name":              "core",
					"sshAuthorizedKeys": []string{sshKey},
				},
			},
		},
		"storage": map[string]any{
			"files": []map[string]any{
				{
					"path": "/etc/extensions/" + flatcarRuntimeName + ".raw",
					"mode": 420,
					"contents": map[string]any{
						"source": "http://10.0.2.2:" + strconv.Itoa(httpPort) + "/" + flatcarRuntimeName + ".raw",
						"verification": map[string]any{
							"hash": "sha512-" + sysextHash,
						},
					},
				},
			},
		},
	}

	dt, err := json.MarshalIndent(ignition, "", "\t")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, dt, 0o644); err != nil {
		t.Fatal(err)
	}
}

func startFlatcarArtifactServer(t *testing.T, dir string) (int, func()) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	srv := &http.Server{
		Handler: http.FileServer(http.Dir(dir)),
	}
	done := make(chan error, 1)
	go func() {
		done <- srv.Serve(ln)
	}()

	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Logf("error shutting down Flatcar artifact HTTP server: %v", err)
		}
		if err := <-done; err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Logf("Flatcar artifact HTTP server exited with error: %v", err)
		}
	}

	return ln.Addr().(*net.TCPAddr).Port, shutdown
}

func freeTCPPort(t *testing.T) int {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func startFlatcarQEMU(ctx context.Context, t *testing.T, vmDir, ignitionPath, qemuLogPath string, sshPort int) (func() error, func() (bool, error)) {
	t.Helper()

	qemuLog, err := os.Create(qemuLogPath)
	if err != nil {
		t.Fatal(err)
	}

	args := []string{
		"-i", ignitionPath,
		"-p", strconv.Itoa(sshPort),
	}
	if !kvmAvailable() {
		t.Log("KVM is unavailable; using QEMU software emulation")
		args = append(args, "-s")
	}
	args = append(args, "--", "-nographic")

	cmd := exec.CommandContext(ctx, filepath.Join(vmDir, "flatcar_production_qemu.sh"), args...)
	cmd.Dir = vmDir
	cmd.Stdout = qemuLog
	cmd.Stderr = qemuLog
	if err := cmd.Start(); err != nil {
		qemuLog.Close()
		t.Fatalf("error starting Flatcar QEMU: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
		qemuLog.Close()
	}()

	var waited bool
	var waitErr error
	waitQEMU := func() error {
		if waited {
			return waitErr
		}
		waitErr = <-done
		waited = true
		return waitErr
	}
	qemuExited := func() (bool, error) {
		if waited {
			return true, waitErr
		}
		select {
		case waitErr = <-done:
			waited = true
			return true, waitErr
		default:
			return false, nil
		}
	}

	t.Cleanup(func() {
		if waited {
			return
		}
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = waitQEMU()
	})

	return waitQEMU, qemuExited
}

func kvmAvailable() bool {
	f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

func waitForFlatcarSSH(ctx context.Context, t *testing.T, keyPath string, sshPort int, qemuExited func() (bool, error), qemuLogPath string) {
	t.Helper()

	deadline := time.NewTimer(6 * time.Minute)
	defer deadline.Stop()
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()

	var lastOut []byte
	var lastErr error
	for {
		exited, err := qemuExited()
		if exited {
			t.Fatalf("Flatcar VM exited before SSH became available: %v\nlast SSH output:\n%s\nQEMU output:\n%s", err, lastOut, tailFile(qemuLogPath, 64*1024))
		}

		attemptCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		lastOut, lastErr = runFlatcarSSH(attemptCtx, keyPath, sshPort, "true")
		cancel()
		if lastErr == nil {
			return
		}

		select {
		case <-ctx.Done():
			t.Fatalf("context canceled waiting for Flatcar SSH: %v\nlast SSH output:\n%s\nQEMU output:\n%s", ctx.Err(), lastOut, tailFile(qemuLogPath, 64*1024))
		case <-deadline.C:
			t.Fatalf("timed out waiting for Flatcar SSH: %v\nlast SSH output:\n%s\nQEMU output:\n%s", lastErr, lastOut, tailFile(qemuLogPath, 64*1024))
		case <-tick.C:
		}
	}
}

func runFlatcarSSH(ctx context.Context, keyPath string, sshPort int, command string) ([]byte, error) {
	args := []string{
		"-i", keyPath,
		"-p", strconv.Itoa(sshPort),
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=5",
		"-o", "LogLevel=ERROR",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"core@127.0.0.1",
		command,
	}
	cmd := exec.CommandContext(ctx, "ssh", args...)
	return cmd.CombinedOutput()
}

func runCommand(ctx context.Context, t *testing.T, dir string, name string, args ...string) {
	t.Helper()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s failed: %v\n%s", name, err, out)
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func tailFile(path string, max int) string {
	dt, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("error reading %s: %v", path, err)
	}
	if len(dt) > max {
		dt = dt[len(dt)-max:]
	}
	return string(dt)
}
