package azlinux

import (
	"context"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

var (
	basePackages = []string{
		// TODO(adamperlin): fill in
		"rpm-build",
		"mariner-rpm-macros",
		"build-essential",
		"ca-certificates",
	}

	targets = map[string]gwclient.BuildFunc{
		Mariner2TargetKey: Mariner2Config.Handle,
		AzLinux3TargetKey: Azlinux3Config.Handle,
	}

	defaultAzlinuxRepoPlatform = dalec.RepoPlatformConfig{
		ConfigRoot: "/etc/yum.repos.d",
		GPGKeyRoot: "/etc/pki/rpm-gpg",
		ConfigExt:  ".repo",
	}
)

func Handlers(ctx context.Context, client gwclient.Client, m *frontend.BuildMux) error {
	return frontend.LoadBuiltinTargets(targets)(ctx, client, m)
}
