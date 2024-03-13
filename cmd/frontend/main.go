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

	var mux frontend.BuildMux

	mux.Add(debug.DebugRoute, debug.Handle, nil)

	if err := grpcclient.RunFromEnvironment(ctx, mux.Handler(
		// copy/paster's beware: [frontend.WithTargetForwardingHandler] should not be set except for the root dalec frontend.
		frontend.WithBuiltinHandler(mariner2.DefaultTargetKey, mariner2.Handle),
		frontend.WithBuiltinHandler(windows.DefaultTargetKey, windows.Handle),
		frontend.WithTargetForwardingHandler,
	)); err != nil {
		bklog.L.WithError(err).Fatal("error running frontend")
		os.Exit(137)
	}
}
