package windows

import (
	"github.com/Azure/dalec/frontend"
	"github.com/moby/buildkit/frontend/subrequests/targets"
)

const (
	targetKey = "windowscross"
	outputKey = "windows"
)

func RegisterHandlers() {
	frontend.RegisterHandler(targetKey, targets.Target{
		Name:        "zip",
		Description: "Builds binaries combined into a zip file",
	}, handleZip)

	frontend.RegisterHandler(targetKey, targets.Target{
		Name:        "container",
		Description: "Builds binaries and installs them into a Windows base image",
		Default:     true,
	}, handleContainer)
}
