package jammy

import (
	"context"

	"github.com/Azure/dalec/frontend/deb"
	"github.com/Azure/dalec/frontend/deb/distro"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

const (
	DefaultTargetKey       = "jammy"
	AptCachePrefix         = "jammy"
	JammyWorkerContextName = "dalec-jammy-worker"

	jammyRef  = "mcr.microsoft.com/mirror/docker/library/ubuntu:jammy"
	versionID = "ubuntu22.04"
)

func Handle(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	cfg := &distro.Config{
		ImageRef:           jammyRef,
		AptCachePrefix:     AptCachePrefix,
		VersionID:          versionID,
		ContextRef:         JammyWorkerContextName,
		DefaultOutputImage: jammyRef,
		BuilderPackages: []string{
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
			"debhelper-compat=" + deb.DebHelperCompat,
		},
	}
	return cfg.Handle(ctx, client)
}
