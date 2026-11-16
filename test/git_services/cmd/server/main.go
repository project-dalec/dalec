package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"

	githttp "github.com/AaronO/go-git-http"
	"github.com/AaronO/go-git-http/auth"
	gitservices "github.com/project-dalec/dalec/test/git_services"
)

const passwd = "password"

var (
	eventOut = json.NewEncoder(os.Stdout)
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: git_http_server <command> [args...]")
		fmt.Fprintln(os.Stderr, "commands: serve, cargo-serve, getip")
		os.Exit(1)
	}

	var err error
	switch os.Args[1] {
	case "serve":
		err = runServe()
	case "cargo-serve":
		err = runCargoServe()
	case "getip":
		err = runGetIP()
	default:
		err = fmt.Errorf("unknown command: %s", os.Args[1])
	}

	if err != nil {
		emitError(err)
		os.Exit(1)
	}
}

const cargoFixtureName = "dalec-auth-fixture"
const cargoFixtureVersion = "0.1.0"

// runCargoServe is a small authenticated sparse Cargo registry used by the
// integration tests. It deliberately requires the exact Authorization header
// that Cargo receives from the credential provider, so a missing or malformed
// secret cannot be hidden by a public registry fallback.
func runCargoServe() error {
	args := os.Args[2:]
	if len(args) != 4 {
		return errors.New("usage: git_http_server cargo-serve <cert> <key> <port> <authorization>")
	}

	cert, err := tls.LoadX509KeyPair(args[0], args[1])
	if err != nil {
		return fmt.Errorf("load Cargo registry certificate: %w", err)
	}

	crate, checksum, err := cargoFixtureCrate()
	if err != nil {
		return err
	}

	authorization := args[3]
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != authorization {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		switch {
		case r.URL.Path == "/index/config.json":
			if _, err := fmt.Fprintf(w, `{"dl":"https://%s/crates/{crate}/{version}/download","auth-required":true}`, r.Host); err != nil {
				return
			}
		case strings.HasPrefix(r.URL.Path, "/index/"):
			if _, err := fmt.Fprintf(w, `{"name":%q,"vers":%q,"deps":[],"cksum":%q,"features":{},"yanked":false}`, cargoFixtureName, cargoFixtureVersion, checksum); err != nil {
				return
			}
		case strings.HasPrefix(r.URL.Path, "/crates/") && strings.HasSuffix(r.URL.Path, "/download"):
			w.Header().Set("Content-Type", "application/gzip")
			_, _ = w.Write(crate)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	addr := fmt.Sprintf("0.0.0.0:%s", args[2])
	listener, err := tls.Listen("tcp", addr, &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		_ = listener.Close()
		return fmt.Errorf("unexpected listener address type %T", listener.Addr())
	}
	ip, err := getContainerIP()
	if err != nil {
		_ = listener.Close()
		return fmt.Errorf("failed to get container IP: %w", err)
	}
	emitReady(ip, tcpAddr.Port)

	if err := (&http.Server{Handler: mux}).Serve(listener); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("cargo registry server error: %w", err)
	}
	return nil
}

func cargoFixtureCrate() ([]byte, string, error) {
	var tarball bytes.Buffer
	gz := gzip.NewWriter(&tarball)
	tarWriter := tar.NewWriter(gz)
	files := map[string]string{
		cargoFixtureName + "-" + cargoFixtureVersion + "/Cargo.toml": fmt.Sprintf("[package]\nname = %q\nversion = %q\nedition = \"2021\"\n", cargoFixtureName, cargoFixtureVersion),
		cargoFixtureName + "-" + cargoFixtureVersion + "/src/lib.rs": "pub fn marker() -> &'static str { \"authenticated cargo registry\" }\n",
	}
	for name, contents := range files {
		if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(contents))}); err != nil {
			return nil, "", err
		}
		if _, err := tarWriter.Write([]byte(contents)); err != nil {
			return nil, "", err
		}
	}
	if err := tarWriter.Close(); err != nil {
		return nil, "", err
	}
	if err := gz.Close(); err != nil {
		return nil, "", err
	}
	data := tarball.Bytes()
	sum := sha256.Sum256(data)
	return data, fmt.Sprintf("%x", sum[:]), nil
}

func runServe() error {
	args := os.Args[2:] // skip program name and "serve"
	if len(args) < 2 {
		return errors.New("usage: git_http_server serve <directory> <port>")
	}

	repo := args[0]
	port := args[1]

	gitHandler := githttp.New(repo)
	authr := auth.Authenticator(func(ai auth.AuthInfo) (bool, error) {
		if ai.Push {
			return false, nil
		}

		if ai.Username == "x-access-token" && ai.Password == passwd {
			return true, nil
		}

		return false, nil
	})

	// Bind to all interfaces. A port of "0" lets the kernel pick a free
	// ephemeral port, which avoids collisions when multiple servers run
	// concurrently in a shared network namespace.
	addr := fmt.Sprintf("0.0.0.0:%s", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	// Resolve the port actually bound, which differs from the requested port
	// when "0" was requested.
	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		listener.Close()
		return fmt.Errorf("unexpected listener address type %T", listener.Addr())
	}

	// Get the container's IP address
	ip, err := getContainerIP()
	if err != nil {
		listener.Close()
		return fmt.Errorf("failed to get container IP: %w", err)
	}

	// Emit the ready event with the IP and the actual bound port
	emitReady(ip, tcpAddr.Port)

	http.Handle("/", authr(gitHandler))
	s := &http.Server{}

	if err := s.Serve(listener); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server error: %w", err)
	}

	return nil
}

func runGetIP() error {
	ip, err := getContainerIP()
	if err != nil {
		return err
	}
	fmt.Println(ip)
	return nil
}

// getContainerIP returns the first non-loopback IPv4 address of the container.
func getContainerIP() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}

	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok {
			if ipNet.IP.IsLoopback() {
				continue
			}
			if ipv4 := ipNet.IP.To4(); ipv4 != nil {
				return ipv4.String(), nil
			}
		}
	}

	return "", errors.New("no suitable IP address found")
}

func emitReady(ip string, port int) {
	event := gitservices.ServerEvent{
		Type:  gitservices.EventTypeReady,
		Ready: &gitservices.ReadyEvent{IP: ip, Port: port},
	}
	eventOut.Encode(event) //nolint:errcheck
}

func emitError(err error) {
	event := gitservices.ServerEvent{
		Type:  gitservices.EventTypeError,
		Error: &gitservices.ErrorEvent{Message: err.Error()},
	}
	eventOut.Encode(event) //nolint:errcheck
}
