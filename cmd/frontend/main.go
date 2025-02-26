package main

import (
	_ "embed"
	"flag"
	"fmt"
	"os"

	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/frontend/debug"
	"github.com/Azure/dalec/targets/linux/deb/debian"
	"github.com/Azure/dalec/targets/linux/deb/ubuntu"
	"github.com/Azure/dalec/targets/linux/rpm/almalinux"
	"github.com/Azure/dalec/targets/linux/rpm/azlinux"
	"github.com/Azure/dalec/targets/linux/rpm/rockylinux"
	"github.com/Azure/dalec/targets/windows"
	"github.com/moby/buildkit/frontend/gateway/grpcclient"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/moby/buildkit/util/bklog"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/grpclog"
)

const (
	Package = "github.com/Azure/dalec/cmd/frontend"

	frontendBasename = "frontend"
	credHelperSubcmd = "credential-helper"
)

func init() {
	bklog.L.Logger.SetOutput(os.Stderr)
	grpclog.SetLoggerV2(grpclog.NewLoggerV2WithVerbosity(bklog.L.WriterLevel(logrus.InfoLevel), bklog.L.WriterLevel(logrus.WarnLevel), bklog.L.WriterLevel(logrus.ErrorLevel), 1))
}

func main() {
	fs := flag.CommandLine
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `usage: %s [subcommand [args...]]`, os.Args[0])
	}

	if err := fs.Parse(os.Args); err != nil {
		bklog.L.WithError(err).Fatal("error parsing frontend args")
		os.Exit(137)
	}

	subCmd := fs.Arg(1)

	// each "sub-main" function handles its own exit
	switch subCmd {
	case credHelperSubcmd:
		args := flag.Args()[2:]
		// skip os.Args[0] and "credential-helper"
		gomodMain(args)
	case "", frontendBasename:
		dalecMain()
	default:
		dalecMain()
	}
}

func dalecMain() {
	ctx := appcontext.Context()

	var mux frontend.BuildMux

	mux.Add(debug.DebugRoute, debug.Handle, nil)

	if err := grpcclient.RunFromEnvironment(ctx, mux.Handler(
		// copy/paster's beware: [frontend.WithTargetForwardingHandler] should not be set except for the root dalec frontend.
		azlinux.Handlers,
		frontend.WithBuiltinHandler(windows.DefaultTargetKey, windows.Handle),
		ubuntu.Handlers,
		debian.Handlers,
		almalinux.Handlers,
		rockylinux.Handlers,
		frontend.WithTargetForwardingHandler,
	)); err != nil {
		bklog.L.WithError(err).Fatal("error running frontend")
		os.Exit(137)
	}
}
