package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/project-dalec/dalec/internal/commands"
	"github.com/project-dalec/dalec/internal/plugins"
)

func init() {
	commands.RegisterPlugin(credHelperSubcmd, plugins.CmdHandlerFunc(credentialHelperCmd))
}

const (
	credHelperSubcmd = "credential-helper"

	keyProtocol          = "protocol"
	keyHost              = "host"
	keyPath              = "path"
	keyUsername          = "username"
	keyPassword          = "password"
	keyPasswordExpiryUTC = "password_expiry_utc"
	keyOauthRefreshToken = "oauth_refresh_token"
	keyUrl               = "url"
	keyAuthtype          = "authtype"
	keyCredential        = "credential"
	keyEphemeral         = "ephemeral"
	keyContinue          = "continue"
	keyWwwauthArr        = "wwwauth[]"
	keyCapabilityArr     = "capability[]"
	keyStateArr          = "state[]"

	actionGet   = "get"
	actionStore = "store"
	actionErase = "erase"

	protocolHTTP  = "http"
	protocolHTTPS = "https"

	kindToken  = "token"
	kindHeader = "header"

	authTypeBasic = "basic"
)

const secretDir = "/run/secrets/"

type gitPayload struct {
	protocol          string   `gitCredential:"protocol"`
	host              string   `gitCredential:"host"`
	path              string   `gitCredential:"path"`
	username          string   `gitCredential:"username"`
	password          string   `gitCredential:"password"`
	passwordExpiryUtc string   `gitCredential:"password_expiry_utc"`
	oauthRefreshToken string   `gitCredential:"oauth_refresh_token"`
	url               string   `gitCredential:"url"`
	authtype          string   `gitCredential:"authtype"`
	credential        string   `gitCredential:"credential"`
	ephemeral         string   `gitCredential:"ephemeral"`
	kontinue          string   `gitCredential:"continue"`
	wwwauth           []string `gitCredential:"wwwauth[]"`
	capability        []string `gitCredential:"capability[]"`
	state             []string `gitCredential:"state[]"`
}

func exit1(msgs ...any) {
	fmt.Fprintln(os.Stderr, msgs...)
	os.Exit(1)
}

type credConfig struct {
	kind string
}

func credentialHelperCmd(ctx context.Context, args []string) {
	var cfg credConfig
	fs := flag.NewFlagSet(credHelperSubcmd, flag.ExitOnError)
	fs.Func("kind", "the kind of secret to retrieve (token or header)", readKind(&cfg.kind))

	if err := fs.Parse(args); err != nil {
		exit1("could not parse args", err)
	}

	action := fs.Arg(0)
	payload := readPayload(os.Stdin)

	switch action {
	case actionGet:
	case actionStore, actionErase:
		// send the "continue" signal to git, signifying that we can't satisfy
		// the request. git will use other credential helpers for these
		// actions, if provided by the user.
		sendContinue(&payload)
		os.Exit(0)
	default:
		exit1(fmt.Sprintf("unrecognized action: %q", action))
	}

	if payload.protocol != protocolHTTP && payload.protocol != protocolHTTPS {
		sendContinue(&payload)
		os.Exit(0)
	}

	file := filepath.Join(secretDir, payload.host, cfg.kind)
	secret, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			exit1(fmt.Errorf("secret not found for host %q and kind %q: %w", payload.host, cfg.kind, err))
		}
		exit1(err)
	}

	resp, err := generateResponse(&payload, secret, cfg.kind)
	if err != nil {
		exit1(err)
	}

	fmt.Println(resp)
}

func readKind(kind *string) func(s string) error {
	return func(s string) error {
		switch s {
		case kindToken, kindHeader:
			*kind = s
		default:
			return fmt.Errorf("kind must be `token` or `header`")
		}

		return nil
	}
}

func sendContinue(payload *gitPayload) {
	payload.kontinue = "true"
	resp := printPayload(payload)
	fmt.Println(resp)
}

func readPayload(r io.Reader) gitPayload {
	sc := bufio.NewScanner(r)

	var payload gitPayload
	for sc.Scan() {
		line := sc.Text()
		k, v, ok := strings.Cut(line, "=")

		if !ok {
			exit1("improper payload from git")
		}

		switch k {
		case keyProtocol:
			payload.protocol = v
		case keyHost:
			payload.host = v
		case keyPath:
			payload.path = v
		case keyUsername:
			payload.username = v
		case keyPassword:
			payload.password = v
		case keyPasswordExpiryUTC:
			payload.passwordExpiryUtc = v
		case keyOauthRefreshToken:
			payload.oauthRefreshToken = v
		case keyUrl:
			payload.url = v
		case keyAuthtype:
			payload.authtype = v
		case keyCredential:
			payload.credential = v
		case keyEphemeral:
			payload.ephemeral = v
		case keyContinue:
			payload.kontinue = v
		case keyWwwauthArr:
			payload.wwwauth = append(payload.wwwauth, v)
		case keyCapabilityArr:
			payload.capability = append(payload.capability, v)
		case keyStateArr:
			payload.state = append(payload.state, v)
		default:
			exit1(fmt.Sprintf("unknown key: %q", k))
		}
	}

	return payload
}

func printPayload(payload *gitPayload) string {
	var buf bytes.Buffer

	fill := func(k, v string) {
		if v == "" {
			return
		}

		fmt.Fprintf(&buf, "%s=%s\n", k, v)
	}

	fillArray := func(k string, v []string) {
		for _, vv := range v {
			fill(k, vv)
		}
	}

	fill(keyProtocol, payload.protocol)
	fill(keyPath, payload.path)
	fill(keyUsername, payload.username)
	fill(keyPassword, payload.password)
	fill(keyPasswordExpiryUTC, payload.passwordExpiryUtc)
	fill(keyOauthRefreshToken, payload.oauthRefreshToken)
	fill(keyUrl, payload.url)
	fill(keyAuthtype, payload.authtype)
	fill(keyCredential, payload.credential)
	fill(keyEphemeral, payload.ephemeral)
	fill(keyContinue, payload.kontinue)
	fillArray(keyWwwauthArr, payload.wwwauth)
	fillArray(keyCapabilityArr, payload.capability)
	fillArray(keyStateArr, payload.state)

	return buf.String()
}

func generateResponse(payload *gitPayload, secret []byte, kind string) (string, error) {
	switch kind {
	case kindToken:
		return handleSecretToken(secret, payload)
	case kindHeader:
		return handleSecretHeader(secret, payload)
	default:
		return "", fmt.Errorf("unrecognized authType: %q", kind)
	}

}

func handleSecretHeader(b []byte, payload *gitPayload) (string, error) {
	s := string(b)
	authtype, credential, ok := strings.Cut(s, " ")
	if !ok {
		return "", fmt.Errorf("improperly formatted auth header")
	}

	payload.authtype = authtype
	payload.credential = credential

	return printPayload(payload), nil
}

func handleSecretToken(token []byte, payload *gitPayload) (string, error) {
	var buf bytes.Buffer
	buf.WriteString("x-access-token:")
	buf.Write(token)

	payload.authtype = authTypeBasic
	payload.credential = base64.StdEncoding.EncodeToString(buf.Bytes())

	return printPayload(payload), nil
}
