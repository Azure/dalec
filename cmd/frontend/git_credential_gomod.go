package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Azure/dalec"
	"github.com/goccy/go-yaml"
)

const (
	authTypeToken = iota
	authTypeHeader
)

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

func exit(msg string, code int) {
	out := os.Stderr
	if code == 0 {
		out = os.Stdout
	}
	fmt.Fprintln(out, msg)
	os.Exit(code)
}

func exit1(msg string) {
	exit(msg, 1)
}

func gomodMain() {
	var err error

	if len(os.Args) < 3 {
		msg := fmt.Sprintf("%#v\n", os.Args)
		exit1("an action and config file are required: " + msg)
	}

	configFile := os.Args[1]
	action := os.Args[2]

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
		os.Exit(1)
	}

	auth, err := getHostAuthFromConfigFile(configFile, payload.host)
	if err != nil {
		exit1(err.Error())
	}

	var (
		resp string
	)

	switch {
	case auth.Token != "":
		resp, err = generateResponse(&payload, auth.Token, authTypeToken)
	case auth.Header != "":
		resp, err = generateResponse(&payload, auth.Header, authTypeHeader)
	default:
		sendContinue(&payload)
		os.Exit(0)
	}

	if err != nil {
		exit1(err.Error())
	}

	fmt.Println(resp)
}

func sendContinue(payload *gitPayload) {
	payload.kontinue = "true"
	resp, err := printPayload(payload)
	if err != nil {
		exit1(err.Error())
	}
	fmt.Println(resp)
}

func readPayload(r io.Reader) gitPayload {
	sc := bufio.NewScanner(r)

	var payload gitPayload
	for sc.Scan() {
		line := sc.Text()
		k, v, ok := strings.Cut(line, "=")

		if !ok {
			exit("improper payload from git", 1)
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

func printPayload(payload *gitPayload) (string, error) {
	var buf bytes.Buffer

	var errs []error
	fill := func(k, v string) {
		if v != "" {
			if _, err := fmt.Fprintln(&buf, k+"="+v); err != nil {
				errs = append(errs, err)
			}
		}
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

	return buf.String(), errors.Join(errs...)
}

func getHostAuthFromConfigFile(configFile, hostname string) (*dalec.GomodGitAuth, error) {
	var m map[string]dalec.GomodGitAuth

	b, err := os.ReadFile(configFile)
	if err != nil {
		return nil, err
	}

	if err := yaml.Unmarshal(b, &m); err != nil {
		return nil, err
	}

	auth, ok := m[hostname]
	if !ok {
		return nil, fmt.Errorf("cannot find host in auth config: %q", hostname)
	}

	return &auth, nil
}

func generateResponse(payload *gitPayload, secret string, authType int) (string, error) {
	secretPath := filepath.Join("/run/secrets", secret)
	b, err := os.ReadFile(secretPath)
	if err != nil {
		return "", err
	}

	switch authType {
	case authTypeToken:
		return handleSecretToken(b, payload)
	case authTypeHeader:
		return handleSecretHeader(b, payload)
	}

	return "", fmt.Errorf("unrecognized authType: %d", authType)
}

func handleSecretHeader(b []byte, payload *gitPayload) (string, error) {
	s := string(b)
	authtype, credential, ok := strings.Cut(s, " ")
	if !ok {
		return "", fmt.Errorf("improperly formatted auth header")
	}

	payload.authtype = authtype
	payload.credential = credential

	return printPayload(payload)
}

func handleSecretToken(b []byte, payload *gitPayload) (string, error) {
	var buf bytes.Buffer
	if _, err := buf.WriteString("x-access-token:"); err != nil {
		return "", err
	}

	if _, err := buf.Write(b); err != nil {
		return "", err
	}

	payload.authtype = "basic"
	payload.credential = base64.StdEncoding.EncodeToString(buf.Bytes())

	return printPayload(payload)
}
