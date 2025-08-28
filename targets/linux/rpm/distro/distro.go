package distro

import (
	"context"
	"path/filepath"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/targets/linux"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests/targets"
)

type Config struct {
	FullName   string
	ImageRef   string
	ContextRef string

	// The release version of the distro
	ReleaseVer string

	// Build dependencies needed
	BuilderPackages []string

	// Dependencies to install in base image
	BasePackages       []dalec.Spec
	RepoPlatformConfig *dalec.RepoPlatformConfig

	DefaultOutputImage string

	InstallFunc PackageInstaller

	// Unique identifier for the package cache for this particular distro,
	// e.g., azlinux3-tdnf-cache
	CacheName string

	// e.g. /var/cache/tdnf or /var/cache/dnf
	CacheDir string

	// erofs-utils 1.7+ is required for tar support.
	SysextSupported bool
}

func (cfg *Config) PackageCacheMount(root string) llb.RunOption {
	return llb.AddMount(filepath.Join(root, cfg.CacheDir), llb.Scratch(), llb.AsPersistentCacheDir(cfg.CacheName, llb.CacheMountLocked))
}

func (c *Config) Install(pkgs []string, opts ...DnfInstallOpt) llb.RunOption {
	var cfg dnfInstallConfig
	dnfInstallOptions(&cfg, opts)

	return dalec.WithRunOptions(c.InstallFunc(&cfg, c.ReleaseVer, pkgs), c.PackageCacheMount(cfg.root))
}

func (cfg *Config) Handle(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	var mux frontend.BuildMux

	mux.Add("rpm", linux.HandlePackage(cfg), &targets.Target{
		Name:        "rpm",
		Description: "Builds an rpm and src.rpm.",
	})

	mux.Add("rpm/debug", cfg.HandleDebug(), &targets.Target{
		Name:        "rpm/debug",
		Description: "Debug options for rpm builds.",
	})

	mux.Add("container", linux.HandleContainer(cfg), &targets.Target{
		Name:        "container",
		Description: "Builds a container image for " + cfg.FullName,
		Default:     true,
	})

	mux.Add("container/depsonly", cfg.HandleDepsOnly, &targets.Target{
		Name:        "container/depsonly",
		Description: "Builds a container image with only the runtime dependencies installed.",
	})

	mux.Add("worker", cfg.HandleWorker, &targets.Target{
		Name:        "worker",
		Description: "Builds the base worker image responsible for building the rpm",
	})

	if cfg.SysextSupported {
		mux.Add("testing/sysext", linux.HandleSysext(cfg), &targets.Target{
			Name:        "testing/sysext",
			Description: "Builds a systemd system extension image.",
		})
	}

	return mux.Handle(ctx, client)
}
