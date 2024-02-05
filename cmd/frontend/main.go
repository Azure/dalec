package main

import (
	_ "embed"
	"os"

	"github.com/moby/buildkit/frontend/gateway/grpcclient"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/moby/buildkit/util/bklog"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/grpclog"

	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/frontend/debug"
	"github.com/Azure/dalec/frontend/mariner2"
	"github.com/Azure/dalec/frontend/windows"
)

const (
	Package = "github.com/Azure/dalec/cmd/frontend"
)

func main() {
	bklog.L.Logger.SetOutput(os.Stderr)
	grpclog.SetLoggerV2(grpclog.NewLoggerV2WithVerbosity(bklog.L.WriterLevel(logrus.InfoLevel), bklog.L.WriterLevel(logrus.WarnLevel), bklog.L.WriterLevel(logrus.ErrorLevel), 1))

	ctx := appcontext.Context()

	debug.RegisterHandlers()
	mariner2.RegisterHandlers()
	windows.RegisterHandlers()

	if err := grpcclient.RunFromEnvironment(ctx, frontend.Build); err != nil {
		bklog.L.WithError(err).Fatal("error running frontend")
		os.Exit(137)
	}
}
