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

		type config struct {
			OS string
		}

		cfg := config{}

		switch target {
		case "windowscross", "windows":
			cfg.OS = "windows"
		default:
			cfg.OS = "linux"
		}

		curFrontend, ok := c.(frontend.CurrentFrontend)
		if !ok {
			return nil, fmt.Errorf("cast to currentFrontend failed")
		}

		basePtr, err := curFrontend.CurrentFrontend()
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

		configBytes, err := json.Marshal(&cfg)
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
