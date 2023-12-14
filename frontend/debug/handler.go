package debug

import (
	"github.com/Azure/dalec/frontend"
	"github.com/moby/buildkit/frontend/subrequests/targets"
)

func RegisterHandlers() {
	frontend.RegisterHandler("debug", targets.Target{
		Name:        "resolve",
		Description: "Outputs the resolved dalec spec file with build args applied.",
	}, HandleResolve)

	frontend.RegisterHandler("debug", targets.Target{
		Name:        "sources",
		Description: "Outputs all sources from a dalec spec file.",
	}, HandleSources)
}
