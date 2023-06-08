package main

import (
	_ "embed"
	"flag"
	"fmt"
	"os"

	"github.com/azure/dalec/frontend/mariner2"
	"github.com/moby/buildkit/frontend/gateway/grpcclient"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/moby/buildkit/util/bklog"
)

const (
	Package = "github.com/azure/dalec/cmd/frontend-mariner2"
)

var (
	//go:embed version.txt
	Version string

	//go:embed revision.txt
	Revision string
)

func main() {
	var version bool
	flag.BoolVar(&version, "version", false, "show version")
	flag.Parse()

	if version {
		fmt.Printf("%s %s %s %s\n", os.Args[0], Package, Version, Revision)
		os.Exit(0)
	}

	if err := grpcclient.RunFromEnvironment(appcontext.Context(), mariner2.Build); err != nil {
		bklog.L.Errorf("fatal error: %+v", err)
		panic(err)
	}
}
