package windows

import (
	"github.com/Azure/dalec/frontend"
	"github.com/moby/buildkit/frontend/subrequests/targets"
)

const (
	targetKey = "windows"
)

func RegisterHandlers() {
	frontend.RegisterHandler(targetKey, targets.Target{
		Name:        "zip",
		Description: "Builds binaries combined into a zip file",
	}, handleZip)
}
