package distro

import (
	"context"
	"encoding/json"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
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
}

func (d *Config) BuildImageConfig(ctx context.Context, resolver llb.ImageMetaResolver, spec *dalec.Spec, platform *ocispecs.Platform, targetKey string) (*dalec.DockerImageSpec, error) {
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

func (d *Config) RepoMounts(repos []dalec.PackageRepositoryConfig, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) (llb.RunOption, error) {
	opts = append(opts, dalec.ProgressGroup("Prepare custom repos"))

	repoConfig := d.RepoPlatformConfig
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

func (d *Config) Worker(sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, error) {
	opts = append(opts, dalec.ProgressGroup("Prepare worker image"))
	if d.ContextRef != "" {
		st, err := sOpt.GetContext(d.ContextRef, dalec.WithConstraints(opts...))
		if err != nil {
			return llb.Scratch(), err
		}
		if st != nil {
			return *st, nil
		}
	}

	base := frontend.GetBaseImage(sOpt, d.ImageRef).
		Run(
			dalec.WithConstraints(opts...),
			AptInstall(d.BuilderPackages...),
		).
		// This file prevents installation of things like docs in ubuntu
		// containers We don't want to exclude this because tests want to
		// check things for docs in the build container. But we also don't
		// want to remove this completely from the base worker image in the
		// frontend because we usually don't want such things in the build
		// environment. This is only needed because certain tests (which
		// are using this customized builder image) are checking for files
		// that are being excluded by this config file.
		File(llb.Rm("/etc/dpkg/dpkg.cfg.d/excludes", llb.WithAllowNotFound(true)))

	return base, nil
}
