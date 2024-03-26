package jammy

import (
	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/frontend/deb"
	"github.com/moby/buildkit/frontend/subrequests/targets"
)

const targetKey = "jammy"

func RegisterHandlers() {
	deb.RegisterHandlers(targetKey)

	frontend.RegisterHandler(targetKey, targets.Target{
		Name:        "deb",
		Default:     true,
		Description: `Build a deb package.`,
	}, handleDeb)
	frontend.RegisterHandler(targetKey, targets.Target{
		Name:        "container",
		Description: `Build a container image.`,
	}, handleContainer)
}
