package debug

import (
	"github.com/Azure/dalec/frontend"
	bktargets "github.com/moby/buildkit/frontend/subrequests/targets"
)

func RegisterTargets() {
	frontend.RegisterBuiltin("debug", bktargets.Target{
		Name:        "resolve",
		Description: "Outputs the resolved dalec spec file with build args applied.",
	}, HandleResolve)
}
