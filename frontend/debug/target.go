package debug

import (
	"github.com/azure/dalec/frontend"
	bktargets "github.com/moby/buildkit/frontend/subrequests/targets"
)

func RegisterTargets() {
	frontend.RegisterTarget("debug", bktargets.Target{
		Name:        "resolve",
		Description: "Outputs the resolved dalec spec file with build args applied.",
	}, HandleResolve)
}
