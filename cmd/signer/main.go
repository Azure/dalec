package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"

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

		signOp := ""
		params := []ParameterKV{}

		switch target {
		case "windowscross", "windows":
			signOp = windowsSignOp
			params = append(params, ParameterKV{
				ParameterName:  "CoseFlags",
				ParameterValue: "chainunprotected",
			})
		default:
			signOp = linuxSignOp
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

		inputs, err := c.Inputs(ctx)
		if err != nil {
			return nil, err
		}

		artifacts, ok := inputs["initialstate"]
		if !ok {
			return nil, fmt.Errorf("no artifact state provided to signer")
		}

		base, ok := inputs["context"]
		if !ok {
			return nil, fmt.Errorf("no base signing image provided")
		}

		// In order for this signing image to work, we need
		base = base.File(llb.Mkfile("/script.sh", 0o777, []byte(`
			#!/usr/bin/env bash
			set -exuo pipefail
			export KEYVAULT_NAME=$(< /run/secrets/KEYVAULT_NAME)
			export AZURE_CLIENT_SECRET=$(< /run/secrets/AZURE_CLIENT_SECRET)
			export AZURE_CLIENT_ID=$(< /run/secrets/AZURE_CLIENT_ID)
			export ESRP_KEYCODE=$(< /run/secrets/ESRP_KEYCODE)
			export AZURE_TENANT_ID=$(< /run/secrets/AZURE_TENANT_ID)
			export BUILDER_EMAIL=$(< /run/secrets/BUILDER_EMAIL)
			envsubst < /config_template.json > /config.json

			az login --service-principal -u "$AZURE_CLIENT_ID" -p "$AZURE_CLIENT_SECRET" --tenant "$AZURE_TENANT_ID" --allow-no-subscriptions
			az xsign sign-file --file-name /artifacts/RPMS/x86_64/* --config-file /config.json
			`)))

		base = base.File(llb.Mkfile("/config_template.json", 0o600, configBytes))

		output := base.Run(llb.Args([]string{"bash", "/script.sh"}),
			llb.AddSecret("/run/secrets/AZURE_CLIENT_ID", llb.SecretID("AZURE_CLIENT_ID")),
			llb.AddSecret("/run/secrets/KEYVAULT_NAME", llb.SecretID("KEYVAULT_NAME")),
			llb.AddSecret("/run/secrets/AZURE_CLIENT_SECRET", llb.SecretID("AZURE_CLIENT_SECRET")),
			llb.AddSecret("/run/secrets/AZURE_TENANT_ID", llb.SecretID("AZURE_TENANT_ID")),
			llb.AddSecret("/run/secrets/ESRP_KEYCODE", llb.SecretID("ESRP_KEYCODE")),
			llb.AddSecret("/run/secrets/BUILDER_EMAIL", llb.SecretID("BUILDER_EMAIL")),
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
