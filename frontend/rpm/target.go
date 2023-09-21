package rpm

import (
	"github.com/azure/dalec/frontend"
	bktargets "github.com/moby/buildkit/frontend/subrequests/targets"
)

func RegisterTargets() {
	frontend.RegisterTarget("rpm", bktargets.Target{
		Name:        "buildroot",
		Description: "Outputs an rpm buildroot suitable for passing to rpmbuild.",
	}, HandleBuildRoot)
	frontend.RegisterTarget("rpm", bktargets.Target{
		Name:        "sources",
		Description: "Outputs all the sources specified in the spec file.",
	}, HandleSources)
	frontend.RegisterTarget("rpm", bktargets.Target{
		Name:        "spec",
		Description: "Outputs the generated RPM spec file",
	}, HandleSpec)
}
