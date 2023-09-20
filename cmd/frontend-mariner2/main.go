package main

import (
	_ "embed"
	"fmt"
	"os"

	"github.com/azure/dalec/frontend/mariner2"
	"github.com/moby/buildkit/frontend/gateway/grpcclient"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/moby/buildkit/util/bklog"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/grpclog"
)

const (
	Package = "github.com/azure/dalec/cmd/mariner2"
)

func main() {
	bklog.L.Logger.SetOutput(os.Stderr)
	grpclog.SetLoggerV2(grpclog.NewLoggerV2WithVerbosity(bklog.L.WriterLevel(logrus.InfoLevel), bklog.L.WriterLevel(logrus.WarnLevel), bklog.L.WriterLevel(logrus.ErrorLevel), 1))

	if len(os.Args) > 1 {
		// Handle re-exec commands here
		// Useful for holding intermediate state without having to use an image or having to include a bunch of extra dependencies in the frontend image.
		if err := handleCmd(os.Args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	if err := grpcclient.RunFromEnvironment(appcontext.Context(), mariner2.Build); err != nil {
		bklog.L.Errorf("fatal error: %+v", err)
		os.Exit(137)
	}
}

func handleCmd(args []string) error {
	switch args[0] {
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}
