package distro

import (
	"context"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests/targets"
)

const (
	PackageManagerTdnf int = iota
	PackageManagerDnf
)

type Config struct {
	ImageRef   string
	ContextRef string

	// The release version of the distro
	ReleaseVer string

	BuilderPackages    []string
	RepoPlatformConfig *dalec.RepoPlatformConfig

	DefaultOutputImage string

	InstallFunc PackageInstaller
}

func (c *Config) Install(pkgs []string, opts ...DnfInstallOpt) llb.RunOption {
	var cfg dnfInstallConfig
	dnfInstallOptions(&cfg, opts)

	return c.InstallFunc(&cfg, c.ReleaseVer, pkgs)
}

func (cfg *Config) Handle(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	var mux frontend.BuildMux

	mux.Add("deb", cfg.HandleRPM, &targets.Target{
		Name:        "rpm",
		Description: "Builds an rpm package.",
		Default:     true,
	})

	mux.Add("testing/container", cfg.HandleContainer, &targets.Target{
		Name:        "testing/container",
		Description: "Builds a container image for testing purposes only.",
	})

	mux.Add("dsc", cfg.HandleSourcePkg, &targets.Target{
		Name:        "dsc",
		Description: "Builds a Debian source package.",
	})

	mux.Add("worker", cfg.HandleWorker, &targets.Target{
		Name:        "worker",
		Description: "Builds the worker image.",
	})

	return mux.Handle(ctx, client)
}
