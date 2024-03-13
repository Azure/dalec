package windows

import (
	"context"

	"github.com/Azure/dalec/frontend"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	bktargets "github.com/moby/buildkit/frontend/subrequests/targets"
)

const (
	DefaultTargetKey = "windowscross"
	outputKey        = "windows"
)

func Handle(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	var mux frontend.BuildMux

	mux.Add("zip", handleZip, &bktargets.Target{
		Name:        "zip",
		Description: "Builds binaries combined into a zip file",
	})

	mux.Add("container", handleContainer, &bktargets.Target{
		Name:        "container",
		Description: "Builds binaries and installs them into a Windows base image",
		Default:     true,
	})
	return mux.Handle(ctx, client)
}
