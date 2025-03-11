package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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

func gomodMain(args []string) {
	var cfg credConfig
	fs := flag.NewFlagSet(credHelperSubcmd, flag.ExitOnError)
	fs.Func("kind", "the kind of secret to retrieve (token or header)", readKind(&cfg.kind))

	if err := fs.Parse(args); err != nil {
		exit1("could not parse args", err)
	}

	action := fs.Arg(0)
	payload := readPayload(os.Stdin)

	switch action {
	case "get":
	case "store", "erase":
		// send the "continue" signal to git, signifying that we can't satisfy
		// the request.
		sendContinue(&payload)
		os.Exit(0)
	default:
		exit1(fmt.Sprintf("unrecognized action: %q", action))
	}

	if payload.protocol != "http" && payload.protocol != "https" {
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
		case "token", "header":
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
		case "protocol":
			payload.protocol = v
		case "host":
			payload.host = v
		case "path":
			payload.path = v
		case "username":
			payload.username = v
		case "password":
			payload.password = v
		case "password_expiry_utc":
			payload.passwordExpiryUtc = v
		case "oauth_refresh_token":
			payload.oauthRefreshToken = v
		case "url":
			payload.url = v
		case "authtype":
			payload.authtype = v
		case "credential":
			payload.credential = v
		case "ephemeral":
			payload.ephemeral = v
		case "continue":
			payload.kontinue = v
		case "wwwauth[]":
			payload.wwwauth = append(payload.wwwauth, v)
		case "capability[]":
			payload.capability = append(payload.capability, v)
		case "state[]":
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

	fill("protocol", payload.protocol)
	fill("path", payload.path)
	fill("username", payload.username)
	fill("password", payload.password)
	fill("password_expiry_utc", payload.passwordExpiryUtc)
	fill("oauth_refresh_token", payload.oauthRefreshToken)
	fill("url", payload.url)
	fill("authtype", payload.authtype)
	fill("credential", payload.credential)
	fill("ephemeral", payload.ephemeral)
	fill("continue", payload.kontinue)
	fillArray("wwwauth[]", payload.wwwauth)
	fillArray("capability[]", payload.capability)
	fillArray("state[]", payload.state)

	return buf.String()
}

func generateResponse(payload *gitPayload, secret []byte, kind string) (string, error) {
	switch kind {
	case "token":
		return handleSecretToken(secret, payload)
	case "header":
		return handleSecretHeader(secret, payload)
	}

	return "", fmt.Errorf("unrecognized authType: %q", kind)
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

	payload.authtype = "basic"
	payload.credential = base64.StdEncoding.EncodeToString(buf.Bytes())

	return printPayload(payload), nil
}
