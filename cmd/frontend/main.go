package main

import (
	"context"
	_ "embed"
	"os"

	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/frontend/azlinux"
	"github.com/Azure/dalec/frontend/debian"
	"github.com/Azure/dalec/frontend/debug"
	"github.com/Azure/dalec/frontend/ubuntu"
	"github.com/Azure/dalec/frontend/windows"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/gateway/grpcclient"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/moby/buildkit/util/bklog"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/grpclog"
)

const (
	Package = "github.com/Azure/dalec/cmd/frontend"
)

func main() {
	bklog.L.Logger.SetOutput(os.Stderr)
	grpclog.SetLoggerV2(grpclog.NewLoggerV2WithVerbosity(bklog.L.WriterLevel(logrus.InfoLevel), bklog.L.WriterLevel(logrus.WarnLevel), bklog.L.WriterLevel(logrus.ErrorLevel), 1))

	ctx := appcontext.Context()

	var mux frontend.BuildMux

	mux.Add(debug.DebugRoute, debug.Handle, nil)

	f := func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
		if err := waitForDebug(ctx); err != nil {
			return nil, err
		}

		handlerFunc := mux.Handler(
			// copy/paster's beware: [frontend.WithTargetForwardingHandler] should not be set except for the root dalec frontend.
			frontend.WithBuiltinHandler(azlinux.Mariner2TargetKey, azlinux.NewMariner2Handler()),
			frontend.WithBuiltinHandler(azlinux.AzLinux3TargetKey, azlinux.NewAzlinux3Handler()),
			frontend.WithBuiltinHandler(windows.DefaultTargetKey, windows.Handle),
			ubuntu.Handlers,
			debian.Handlers,
			frontend.WithTargetForwardingHandler,
		)

		return handlerFunc(ctx, client)
	}

	if err := grpcclient.RunFromEnvironment(ctx, f); err != nil {
		bklog.L.WithError(err).Fatal("error running frontend")
		os.Exit(137)
	}

}
