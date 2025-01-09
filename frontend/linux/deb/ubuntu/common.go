package ubuntu

import (
	"context"

	"github.com/Azure/dalec/frontend"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

var (
	basePackages = []string{
		"aptitude",
		"dpkg-dev",
		"devscripts",
		"equivs",
		"fakeroot",
		"dh-make",
		"build-essential",
		"dh-apparmor",
		"dh-make",
		"dh-exec",
	}

	targets = map[string]gwclient.BuildFunc{
		BionicDefaultTargetKey: BionicConfig.Handle, // 18.04
		FocalDefaultTargetKey:  FocalConfig.Handle,  // 20.04
		JammyDefaultTargetKey:  JammyConfig.Handle,  // 22.04
		NobleDefaultTargetKey:  NobleConfig.Handle,  // 24.04
	}
)

func Handlers(ctx context.Context, client gwclient.Client, m *frontend.BuildMux) error {
	return frontend.LoadBuiltinTargets(targets)(ctx, client, m)
}
