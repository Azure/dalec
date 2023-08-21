package main

import (
	_ "embed"
	"flag"

	"github.com/azure/dalec/frontend/mariner2"
	"github.com/moby/buildkit/frontend/gateway/grpcclient"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/moby/buildkit/util/bklog"
)

const (
	Package = "github.com/azure/dalec/cmd/frontend-mariner2"
)

func main() {
	var version bool
	flag.BoolVar(&version, "version", false, "show version")
	flag.Parse()

	if err := grpcclient.RunFromEnvironment(appcontext.Context(), mariner2.Build); err != nil {
		bklog.L.Errorf("fatal error: %+v", err)
		panic(err)
	}
}
