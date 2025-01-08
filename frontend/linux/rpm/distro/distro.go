package distro

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests/targets"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
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
	BasePackages       []string
	RepoPlatformConfig *dalec.RepoPlatformConfig

	DefaultOutputImage string

	InstallFunc PackageInstaller

	// Unique identifier for the package cache for this particular distro,
	// e.g., azlinux3-tdnf-cache
	CacheName string

	// e.g. /var/cache/tdnf or /var/cache/dnf
	CacheDir string
}

func (cfg *Config) PackageCacheMount(root string) llb.RunOption {
	return llb.AddMount(filepath.Join(root, cfg.CacheDir), llb.Scratch(), llb.AsPersistentCacheDir(cfg.CacheName, llb.CacheMountLocked))
}

func (cfg *Config) BuildImageConfig(ctx context.Context, resolver llb.ImageMetaResolver, spec *dalec.Spec, platform *ocispecs.Platform, targetKey string) (*dalec.DockerImageSpec, error) {
	img, err := resolveConfig(ctx, resolver, spec, platform, targetKey)
	if err != nil {
		return nil, err
	}

	if err := dalec.BuildImageConfig(spec, targetKey, img); err != nil {
		return nil, err
	}

	return img, nil
}

func resolveConfig(ctx context.Context, resolver llb.ImageMetaResolver, spec *dalec.Spec, platform *ocispecs.Platform, targetKey string) (*dalec.DockerImageSpec, error) {
	ref := dalec.GetBaseOutputImage(spec, targetKey)
	if ref == "" {
		return dalec.BaseImageConfig(platform), nil
	}

	_, _, dt, err := resolver.ResolveImageConfig(ctx, ref, sourceresolver.Opt{
		Platform: platform,
	})
	if err != nil {
		return nil, err
	}

	var img dalec.DockerImageSpec
	if err := json.Unmarshal(dt, &img); err != nil {
		return nil, errors.Wrap(err, "error unmarshalling base image config")
	}
	return &img, nil
}

func (c *Config) Install(pkgs []string, opts ...DnfInstallOpt) llb.RunOption {
	var cfg dnfInstallConfig
	dnfInstallOptions(&cfg, opts)

	return dalec.WithRunOptions(c.InstallFunc(&cfg, c.ReleaseVer, pkgs), c.PackageCacheMount(cfg.root))
}

func (cfg *Config) Handle(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	var mux frontend.BuildMux

	mux.Add("rpm", cfg.HandleRPM, &targets.Target{
		Name:        "rpm",
		Description: "Builds an rpm and src.rpm.",
	})

	mux.Add("rpm/debug", cfg.HandleDebug(), &targets.Target{
		Name:        "rpm/debug",
		Description: "Debug options for rpm builds.",
	})

	mux.Add("container", cfg.HandleContainer, &targets.Target{
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

	return mux.Handle(ctx, client)
}
