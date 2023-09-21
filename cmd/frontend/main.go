package main

import (
	_ "embed"
	"os"

	"github.com/azure/dalec/frontend"
	"github.com/moby/buildkit/frontend/gateway/grpcclient"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/moby/buildkit/util/bklog"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/grpclog"

	_ "github.com/azure/dalec/frontend/register" // register all known targets
)

const (
	Package = "github.com/azure/dalec/cmd/mariner2"
)

func main() {
	bklog.L.Logger.SetOutput(os.Stderr)
	grpclog.SetLoggerV2(grpclog.NewLoggerV2WithVerbosity(bklog.L.WriterLevel(logrus.InfoLevel), bklog.L.WriterLevel(logrus.WarnLevel), bklog.L.WriterLevel(logrus.ErrorLevel), 1))

	if err := grpcclient.RunFromEnvironment(appcontext.Context(), frontend.Build); err != nil {
		bklog.L.Errorf("fatal error: %+v", err)
		os.Exit(137)
	}
}
