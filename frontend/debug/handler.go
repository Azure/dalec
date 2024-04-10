package debug

import (
	"context"

	"github.com/Azure/dalec/frontend"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests/targets"
)

const DebugRoute = "debug"

func Handle(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	var r frontend.BuildMux

	r.Add("resolve", Resolve, &targets.Target{
		Name:        "resolve",
		Description: "Outputs the resolved dalec spec file with build args applied.",
	})
	r.Add("sources", Sources, &targets.Target{
		Name:        "sources",
		Description: "Outputs all sources from a dalec spec file.",
	})
	r.Add("gomods", Gomods, &targets.Target{
		Name:        "gomods",
		Description: "Outputs all the gomodule dependencies for the spec",
	})

	return r.Handle(ctx, client)
}
