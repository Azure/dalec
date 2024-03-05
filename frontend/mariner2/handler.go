package mariner2

import (
	"fmt"

	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/frontend/rpm"
	"github.com/moby/buildkit/frontend/subrequests/targets"
)

const (
	targetKey = "mariner2"
)

func RegisterHandlers() {
	frontend.RegisterHandler(frontend.HandlerKey{ Path: fmt.Sprintf("%s/%s", targetKey, "rpm"), Group: targetKey }, targets.Target{
		Name:        "rpm",
		Description: "Builds an rpm and src.rpm for mariner2.",
	}, handleRPM)

	frontend.RegisterHandler(frontend.HandlerKey{ Path: fmt.Sprintf("%s/%s", targetKey, "container"), Group: targetKey }, targets.Target{
		Name:        "container",
		Description: "Builds a container with the RPM installed.",
		Default:     true,
	}, handleContainer)

	frontend.RegisterHandler(frontend.HandlerKey{ Path: fmt.Sprintf("%s/%s", targetKey, "container/depsonly"), Group: targetKey }, targets.Target{
		Name:        "container/depsonly",
		Description: "Builds a container with only the runtime dependencies installed.",
	}, handleDepsOnly)

	rpm.RegisterHandlers(targetKey)
}
