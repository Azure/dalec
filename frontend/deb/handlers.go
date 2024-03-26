package deb

import (
	"path"

	"github.com/Azure/dalec/frontend"
	"github.com/moby/buildkit/frontend/subrequests/targets"
)

func RegisterHandlers(group string) {
	frontend.RegisterHandler(path.Join(group, "deb"), targets.Target{
		Name:        "control",
		Description: "Outputs the generated debian control file",
	}, ControlHandler(group))

	frontend.RegisterHandler(path.Join(group, "deb"), targets.Target{
		Name:        "debroot",
		Description: "Outputs the full debian directory which can be used with dpkg-buildpackage",
	}, DebrootHandler(group))

}
