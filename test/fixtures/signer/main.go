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

		inputs, err := c.Inputs(ctx)
		if err != nil {
			return nil, err
		}

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

		cc, ok := c.(frontend.CurrentFrontend)
		if !ok {
			return nil, fmt.Errorf("cast to currentFrontend failed")
		}

		basePtr, err := cc.CurrentFrontend()
		if err != nil || basePtr == nil {
			if err == nil {
				err = fmt.Errorf("base frontend ptr was nil")
			}
			return nil, err
		}

		inputId := strings.TrimPrefix(bopts["context"], "input:")
		_, ok = inputs[inputId]
		if !ok {
			return nil, fmt.Errorf("no artifact state provided to signer")
		}

		configBytes, err := json.Marshal(&config)
		if err != nil {
			return nil, err
		}

		output := llb.Scratch().
			File(llb.Mkfile("/target", 0o600, []byte(target))).
			File(llb.Mkfile("/config.json", 0o600, configBytes))

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
