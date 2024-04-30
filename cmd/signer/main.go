package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/gateway/grpcclient"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/moby/buildkit/util/bklog"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/grpclog"
)

const (
	linuxSignOp   = "LinuxSign"
	windowsSignOp = "NotaryCoseSign"
)

type Config struct {
	ClientId           string             `json:"clientId"`
	GatewayAPI         string             `json:"gatewayApi"`
	RequestSigningCert map[string]string  `json:"requestSigningCert"`
	DRIEmail           []string           `json:"driEmail"`
	SigningOperations  []SigningOperation `json:"signingOperations"`
	HashType           string             `json:"hashType"`
}

type SigningOperation struct {
	KeyCode          string        `json:"keyCode"`
	OperationSetCode string        `json:"operationSetCode"`
	Parameters       []ParameterKV `json:"parameters"`
	ToolName         string        `json:"toolName"`
	ToolVersion      string        `json:"toolVersion"`
}

type ParameterKV struct {
	ParameterName  string `json:"parameterName"`
	ParameterValue string `json:"parameterValue"`
}

func main() {
	bklog.L.Logger.SetOutput(os.Stderr)
	grpclog.SetLoggerV2(grpclog.NewLoggerV2WithVerbosity(bklog.L.WriterLevel(logrus.InfoLevel), bklog.L.WriterLevel(logrus.WarnLevel), bklog.L.WriterLevel(logrus.ErrorLevel), 1))

	ctx := appcontext.Context()

	if err := grpcclient.RunFromEnvironment(ctx, func(ctx context.Context, c gwclient.Client) (*gwclient.Result, error) {
		bopts := c.BuildOpts().Opts
		target := bopts["dalec.target"]

		cc, ok := c.(frontend.CurrentFrontend)
		if !ok {
			return nil, fmt.Errorf("cast to currentFrontend failed")
		}

		basePtr, err := cc.CurrentFrontend()
		if err != nil {
			return nil, err
		}
		base := *basePtr

		inputs, err := c.Inputs(ctx)
		if err != nil {
			return nil, err
		}

		inputId := strings.TrimPrefix(bopts["context"], "input:")
		artifacts, ok := inputs[inputId]
		if !ok {
			return nil, fmt.Errorf("no artifact state provided to signer")
		}

		signOp := ""
		params := []ParameterKV{}
		findPattern := llb.Scratch()

		switch target {
		case "windowscross", "windows":
			signOp = windowsSignOp
			params = append(params, ParameterKV{
				ParameterName:  "CoseFlags",
				ParameterValue: "chainunprotected",
			})
			findPattern = base.
				Run(llb.Args([]string{"bash", "-c",
					`find /artifacts -type f -regextype posix-egrep regex '.*\.(exe|ps1)$' -print0 > /tmp/output/artifacts_list`})).
				AddMount("/tmp/output/", llb.Scratch())
		case "mariner2", "jammy":
			signOp = linuxSignOp
			findPattern = base.Run(llb.Args([]string{"bash", "-c",
				`find /artifacts -type f -regextype posix-egrep regex '.*\.(rpm)$' -print0 > /tmp/output/artifacts_list`})).
				AddMount("/tmp/output/", llb.Scratch())

		}

		config := Config{
			ClientId:   "${AZURE_CLIENT_ID}",
			GatewayAPI: "https://api.esrp.microsoft.com",
			RequestSigningCert: map[string]string{
				"subject":   "esrp-prss",
				"vaultName": "${KEYVAULT_NAME}",
			},
			DRIEmail: []string{"${BUILDER_EMAIL}"},
			SigningOperations: []SigningOperation{
				{
					KeyCode:          "${ESRP_KEYCODE}",
					OperationSetCode: signOp,
					Parameters:       params,
					ToolName:         "sign",
					ToolVersion:      "1.0",
				},
			},
			HashType: "sha256",
		}

		configBytes, err := json.Marshal(&config)
		if err != nil {
			return nil, err
		}

		// In order for this signing image to work, we need
		base = base.File(llb.Mkfile("/script.sh", 0o777, []byte(`
			#!/usr/bin/env bash
			set -exuo pipefail
			: "${FIND_PATTERN}"

			(
				# avoid leaking secrets
				set +x
				envsubst < /config_template.json > /config.json
				az login --service-principal \
					-username "$AZURE_CLIENT_ID" \
					-password "$AZURE_CLIENT_SECRET" \
					--tenant "$AZURE_TENANT_ID" \
					--allow-no-subscriptions
			)
			
			readarray -d '' artifacts < <(find /artifacts -type f ${FIND_PATTERN:+-name "$FIND_PATTERN"} -print0)
			for f in "${artifacts[@]}"; do
				az xsign sign-file --file-name "$f" --config-file /config.json
			done
			`)))

		base = base.File(llb.Mkfile("/config_template.json", 0o600, configBytes))

		output := base.Run(llb.Args([]string{"bash", "/script.sh"}),
			llb.AddEnv("FIND_PATTERN", findPattern),
			secretToEnv("AZURE_CLIENT_ID"),
			secretToEnv("KEYVAULT_NAME"),
			secretToEnv("AZURE_CLIENT_SECRET"),
			secretToEnv("AZURE_TENANT_ID"),
			secretToEnv("ESRP_KEYCODE"),
			secretToEnv("BUILDER_EMAIL"),
		).AddMount("/artifacts", artifacts)

		def, err := output.Marshal(ctx)
		if err != nil {
			return nil, err
		}

		return c.Solve(ctx, gwclient.SolveRequest{
			Definition: def.ToPB(),
		})
	}); err != nil {
		bklog.L.WithError(err).Fatal("error running frontend")
		os.Exit(137)
	}
}

func secretToEnv(secretName string) llb.RunOption {
	return llb.AddSecret(secretName, llb.SecretID(secretName), llb.SecretAsEnv(true))
}
