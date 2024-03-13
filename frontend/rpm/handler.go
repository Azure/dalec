package rpm

import (
	"context"

	"github.com/Azure/dalec/frontend"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests/targets"
)

// HandleDebug returns a build function that adds support for some debugging targets for RPM builds.
func HandleDebug() gwclient.BuildFunc {
	return func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
		var r frontend.BuildMux

		r.Add("buildroot", HandleBuildroot(), &targets.Target{
			Name:        "buildroot",
			Description: "Outputs an rpm buildroot suitable for passing to rpmbuild.",
		})

		r.Add("sources", HandleSources(), &targets.Target{
			Name:        "sources",
			Description: "Outputs all the sources specified in the spec file in the format given to rpmbuild.",
		})

		r.Add("spec", HandleSpec(), &targets.Target{
			Name:        "spec",
			Description: "Outputs the generated RPM spec file",
		})

		return r.Handle(ctx, client)
	}
}
