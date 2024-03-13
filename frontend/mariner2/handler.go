package mariner2

import (
	"context"

	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/frontend/rpm"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	bktargets "github.com/moby/buildkit/frontend/subrequests/targets"
)

const (
	DefaultTargetKey = "mariner2"
)

func Handle(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	var mux frontend.BuildMux

	mux.Add("rpm", handleRPM, &bktargets.Target{
		Name:        "rpm",
		Description: "Builds an rpm and src.rpm for mariner2.",
	})
	mux.Add("rpm/debug", rpm.HandleDebug(), nil)

	mux.Add("container", handleContainer, &bktargets.Target{
		Name:        "container",
		Description: "Builds a container image for mariner2.",
		Default:     true,
	})

	mux.Add("container/depsonly", handleDepsOnly, &bktargets.Target{
		Name:        "container/depsonly",
		Description: "Builds a container image with only the runtime dependencies installed.",
	})

	return mux.Handle(ctx, client)
}
