package rpm

import (
	"path"

	"github.com/Azure/dalec/frontend"
	bktargets "github.com/moby/buildkit/frontend/subrequests/targets"
)

func RegisterHandlers(group string) {
	frontend.RegisterHandler(path.Join(group, "rpm"), bktargets.Target{
		Name:        "buildroot",
		Description: "Outputs an rpm buildroot suitable for passing to rpmbuild.",
	}, BuildrootHandler(group))
	frontend.RegisterHandler(path.Join(group, "rpm"), bktargets.Target{
		Name:        "sources",
		Description: "Outputs all the sources specified in the spec file.",
	}, HandleSources)
	frontend.RegisterHandler(path.Join(group, "rpm"), bktargets.Target{
		Name:        "spec",
		Description: "Outputs the generated RPM spec file",
	}, SpecHandler(group))
}
