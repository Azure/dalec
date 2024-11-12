package distro

import (
	"context"
	"encoding/json"

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
	ImageRef       string
	ContextRef     string
	VersionID      string
	AptCachePrefix string

	BuilderPackages    []string
	RepoPlatformConfig *dalec.RepoPlatformConfig

	DefaultOutputImage string
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

func (cfg *Config) RepoMounts(repos []dalec.PackageRepositoryConfig, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) (llb.RunOption, error) {
	opts = append(opts, dalec.ProgressGroup("Prepare custom repos"))

	repoConfig := cfg.RepoPlatformConfig
	if repoConfig == nil {
		repoConfig = &dalec.RepoPlatformConfig{
			ConfigRoot: "/etc/apt/sources.list.d",
			GPGKeyRoot: "/usr/share/keyrings",
			ConfigExt:  ".list",
		}
	}

	withRepos, err := dalec.WithRepoConfigs(repos, repoConfig, sOpt, opts...)
	if err != nil {
		return nil, err
	}

	withData, err := dalec.WithRepoData(repos, sOpt, opts...)
	if err != nil {
		return nil, err
	}

	keyMounts, _, err := dalec.GetRepoKeys(repos, repoConfig, sOpt, opts...)
	if err != nil {
		return nil, err
	}

	return dalec.WithRunOptions(withRepos, withData, keyMounts), nil
}

func (cfg *Config) Handle(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	var mux frontend.BuildMux

	mux.Add("deb", cfg.HandleDeb, &targets.Target{
		Name:        "deb",
		Description: "Builds a deb package.",
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
