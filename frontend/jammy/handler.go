package jammy

import (
	"context"

	"github.com/Azure/dalec/frontend"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests/targets"
)

const (
	DefaultTargetKey = "jammy"
	aptCachePrefix   = "jammy"
)

func Handle(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	var mux frontend.BuildMux

	mux.Add("deb", handleDeb, &targets.Target{
		Name:        "deb",
		Description: "Builds a deb package for jammy.",
	})

	mux.Add("container", handleContainer, &targets.Target{
		Name:        "container",
		Description: "Builds a container image for jammy.",
		Default:     true,
	})

	mux.Add("dsc", handleDebianSourcePackage, &targets.Target{
		Name:        "dsc",
		Description: "Builds a Debian source package for jammy.",
	})

	return mux.Handle(ctx, client)
}
