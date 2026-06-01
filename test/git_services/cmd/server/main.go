package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"

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
		fmt.Fprintln(os.Stderr, "commands: serve, getip")
		os.Exit(1)
	}

	var err error
	switch os.Args[1] {
	case "serve":
		err = runServe()
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

	// Bind to all interfaces
	addr := fmt.Sprintf("0.0.0.0:%s", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	// Get the container's IP address
	ip, err := getContainerIP()
	if err != nil {
		listener.Close()
		return fmt.Errorf("failed to get container IP: %w", err)
	}

	// Emit the ready event with the IP and port
	emitReady(ip, port)

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

func emitReady(ip, port string) {
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
