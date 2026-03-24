package main

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestHandleSecretToken(t *testing.T) {
	t.Run("without authtype capability", func(t *testing.T) {
		// Pre-2.46 git does not announce capability[]=authtype.
		// The credential helper must respond with username/password,
		// otherwise git silently discards the response and auth fails.
		payload := &gitPayload{
			protocol: "https",
			host:     "github.com",
		}

		resp, err := handleSecretToken([]byte("ghp_abc123"), payload)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		assertFieldEquals(t, resp, keyUsername, "x-access-token")
		assertFieldEquals(t, resp, keyPassword, "ghp_abc123")
		assertFieldAbsent(t, resp, keyAuthtype)
		assertFieldAbsent(t, resp, keyCredential)
	})

	t.Run("with authtype capability", func(t *testing.T) {
		// Git 2.46+ announces capability[]=authtype.
		// The credential helper should use the v2 protocol fields.
		payload := &gitPayload{
			protocol:   "https",
			host:       "github.com",
			capability: []string{"authtype"},
		}

		resp, err := handleSecretToken([]byte("ghp_abc123"), payload)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		assertFieldEquals(t, resp, keyAuthtype, authTypeBasic)

		expectedCred := base64.StdEncoding.EncodeToString([]byte("x-access-token:ghp_abc123"))
		assertFieldEquals(t, resp, keyCredential, expectedCred)
		assertFieldAbsent(t, resp, keyUsername)
		assertFieldAbsent(t, resp, keyPassword)
	})
}

func TestHandleSecretHeader(t *testing.T) {
	t.Run("without authtype capability bearer", func(t *testing.T) {
		// Bearer tokens cannot be expressed via username/password.
		// On pre-2.46 git, this should return an error since there's
		// no way to pass an arbitrary Authorization header.
		payload := &gitPayload{
			protocol: "https",
			host:     "github.com",
		}

		_, err := handleSecretHeader([]byte("Bearer eyJhbGciOi"), payload)
		if err == nil {
			t.Fatal("expected error for Bearer auth without authtype capability, got nil")
		}
	})

	t.Run("without authtype capability basic", func(t *testing.T) {
		// Basic auth headers can be decoded into username/password
		// for pre-2.46 git compatibility.
		payload := &gitPayload{
			protocol: "https",
			host:     "github.com",
		}

		cred := base64.StdEncoding.EncodeToString([]byte("myuser:mypass"))
		resp, err := handleSecretHeader([]byte("Basic "+cred), payload)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		assertFieldEquals(t, resp, keyUsername, "myuser")
		assertFieldEquals(t, resp, keyPassword, "mypass")
		assertFieldAbsent(t, resp, keyAuthtype)
		assertFieldAbsent(t, resp, keyCredential)
	})

	t.Run("with authtype capability", func(t *testing.T) {
		// Git 2.46+ supports authtype/credential for arbitrary auth schemes.
		payload := &gitPayload{
			protocol:   "https",
			host:       "github.com",
			capability: []string{"authtype"},
		}

		resp, err := handleSecretHeader([]byte("Bearer eyJhbGciOi"), payload)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		assertFieldEquals(t, resp, keyAuthtype, "Bearer")
		assertFieldEquals(t, resp, keyCredential, "eyJhbGciOi")
		assertFieldAbsent(t, resp, keyUsername)
		assertFieldAbsent(t, resp, keyPassword)
	})

	t.Run("malformed header", func(t *testing.T) {
		payload := &gitPayload{
			protocol:   "https",
			host:       "github.com",
			capability: []string{"authtype"},
		}

		_, err := handleSecretHeader([]byte("nospaceshere"), payload)
		if err == nil {
			t.Fatal("expected error for malformed header, got nil")
		}
	})
}

func TestReadPayload(t *testing.T) {
	t.Run("valid input", func(t *testing.T) {
		input := "protocol=https\nhost=github.com\npath=foo/bar\n"
		payload := readPayload(strings.NewReader(input))

		if payload.protocol != "https" {
			t.Errorf("expected protocol 'https', got %q", payload.protocol)
		}
		if payload.host != "github.com" {
			t.Errorf("expected host 'github.com', got %q", payload.host)
		}
		if payload.path != "foo/bar" {
			t.Errorf("expected path 'foo/bar', got %q", payload.path)
		}
	})

	t.Run("with capabilities", func(t *testing.T) {
		input := "protocol=https\nhost=github.com\ncapability[]=authtype\ncapability[]=state\n"
		payload := readPayload(strings.NewReader(input))

		if len(payload.capability) != 2 {
			t.Fatalf("expected 2 capabilities, got %d", len(payload.capability))
		}
		if payload.capability[0] != "authtype" {
			t.Errorf("expected capability 'authtype', got %q", payload.capability[0])
		}
		if payload.capability[1] != "state" {
			t.Errorf("expected capability 'state', got %q", payload.capability[1])
		}
	})

	t.Run("unknown keys are skipped", func(t *testing.T) {
		// Per git credential protocol spec, unrecognized attributes
		// should be silently discarded.
		input := "protocol=https\nfuture_key=some_value\nhost=github.com\n"
		payload := readPayload(strings.NewReader(input))

		if payload.protocol != "https" {
			t.Errorf("expected protocol 'https', got %q", payload.protocol)
		}
		if payload.host != "github.com" {
			t.Errorf("expected host 'github.com', got %q", payload.host)
		}
	})

	t.Run("blank line terminates input", func(t *testing.T) {
		// A blank line terminates the credential protocol input.
		input := "protocol=https\nhost=github.com\n\npath=should-not-be-read\n"
		payload := readPayload(strings.NewReader(input))

		if payload.protocol != "https" {
			t.Errorf("expected protocol 'https', got %q", payload.protocol)
		}
		if payload.host != "github.com" {
			t.Errorf("expected host 'github.com', got %q", payload.host)
		}
		if payload.path != "" {
			t.Errorf("expected path to be empty (after blank line), got %q", payload.path)
		}
	})
}

func TestGenerateResponse(t *testing.T) {
	t.Run("token without capability", func(t *testing.T) {
		payload := &gitPayload{
			protocol: "https",
			host:     "github.com",
		}

		resp, err := generateResponse(payload, []byte("my-secret-token"), kindToken)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		assertFieldEquals(t, resp, keyUsername, "x-access-token")
		assertFieldEquals(t, resp, keyPassword, "my-secret-token")
	})

	t.Run("token with capability", func(t *testing.T) {
		payload := &gitPayload{
			protocol:   "https",
			host:       "github.com",
			capability: []string{"authtype"},
		}

		resp, err := generateResponse(payload, []byte("my-secret-token"), kindToken)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		assertFieldEquals(t, resp, keyAuthtype, authTypeBasic)
		expectedCred := base64.StdEncoding.EncodeToString([]byte("x-access-token:my-secret-token"))
		assertFieldEquals(t, resp, keyCredential, expectedCred)
	})

	t.Run("header with capability", func(t *testing.T) {
		payload := &gitPayload{
			protocol:   "https",
			host:       "github.com",
			capability: []string{"authtype"},
		}

		resp, err := generateResponse(payload, []byte("Bearer abc123"), kindHeader)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		assertFieldEquals(t, resp, keyAuthtype, "Bearer")
		assertFieldEquals(t, resp, keyCredential, "abc123")
	})

	t.Run("unknown kind", func(t *testing.T) {
		payload := &gitPayload{
			protocol: "https",
			host:     "github.com",
		}

		_, err := generateResponse(payload, []byte("secret"), "unknown")
		if err == nil {
			t.Fatal("expected error for unknown kind, got nil")
		}
	})
}

// assertFieldEquals checks that a credential protocol response contains
// a specific key=value pair.
func assertFieldEquals(t *testing.T, resp, key, expected string) {
	t.Helper()

	prefix := key + "="
	for _, line := range strings.Split(resp, "\n") {
		if strings.HasPrefix(line, prefix) {
			got := strings.TrimPrefix(line, prefix)
			if got != expected {
				t.Errorf("field %q: expected %q, got %q", key, expected, got)
			}
			return
		}
	}

	t.Errorf("field %q not found in response:\n%s", key, resp)
}

// assertFieldAbsent checks that a credential protocol response does NOT
// contain a specific key.
func assertFieldAbsent(t *testing.T, resp, key string) {
	t.Helper()

	prefix := key + "="
	for _, line := range strings.Split(resp, "\n") {
		if strings.HasPrefix(line, prefix) {
			t.Errorf("field %q should be absent but found: %s", key, line)
			return
		}
	}
}
