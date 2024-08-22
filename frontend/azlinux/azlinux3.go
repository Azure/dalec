package azlinux

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/Azure/dalec"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	AzLinux3TargetKey     = "azlinux3"
	tdnfCacheNameAzlinux3 = "azlinux3-tdnf-cache"

	// Azlinux3Ref is the image ref used for the base worker image
	Azlinux3Ref = "mcr.microsoft.com/azurelinux/base/core:3.0"
	// Azlinux3WorkerContextName is the build context name that can be used to lookup
	Azlinux3WorkerContextName = "dalec-azlinux3-worker"
	azlinux3DistrolessRef     = "mcr.microsoft.com/azurelinux/distroless/base:3.0"
)

func NewAzlinux3Handler() gwclient.BuildFunc {
	return newHandler(azlinux3{})
}

type azlinux3 struct{}

func (w azlinux3) Base(sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, error) {
	base, err := sOpt.GetContext(Azlinux3Ref, dalec.WithConstraints(opts...))
	if err != nil {
		return llb.Scratch(), err
	}

	if base != nil {
		return *base, nil
	}

	base, err = sOpt.GetContext(Azlinux3WorkerContextName, dalec.WithConstraints(opts...))
	if err != nil {
		return llb.Scratch(), nil
	}
	if base != nil {
		return *base, nil
	}

	img := llb.Image(Azlinux3Ref, llb.WithMetaResolver(sOpt.Resolver), dalec.WithConstraints(opts...))
	return img.Run(
		w.Install([]string{"rpm-build", "mariner-rpm-macros", "build-essential", "ca-certificates"}, installWithConstraints(opts)),
		dalec.WithConstraints(opts...),
	).Root(), nil
}

func (w azlinux3) Install(pkgs []string, opts ...installOpt) llb.RunOption {
	var cfg installConfig
	setInstallOptions(&cfg, opts)
	return dalec.WithRunOptions(tdnfInstall(&cfg, "3.0", pkgs), w.tdnfCacheMount(cfg.root))
}

func (w azlinux3) BasePackages() []string {
	return []string{"azurelinux-release"}
}

func (azlinux3) DefaultImageConfig(ctx context.Context, resolver llb.ImageMetaResolver, platform *ocispecs.Platform) (*dalec.DockerImageSpec, error) {
	_, _, dt, err := resolver.ResolveImageConfig(ctx, azlinux3DistrolessRef, sourceresolver.Opt{Platform: platform})
	if err != nil {
		return nil, err
	}

	var cfg dalec.DockerImageSpec
	if err := json.Unmarshal(dt, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (azlinux3) WorkerImageConfig(ctx context.Context, resolver llb.ImageMetaResolver, platform *ocispecs.Platform) (*dalec.DockerImageSpec, error) {
	_, _, dt, err := resolver.ResolveImageConfig(ctx, Azlinux3Ref, sourceresolver.Opt{Platform: platform})
	if err != nil {
		return nil, err
	}

	var cfg dalec.DockerImageSpec
	if err := json.Unmarshal(dt, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (azlinux3) tdnfCacheMount(root string) llb.RunOption {
	return llb.AddMount(filepath.Join(root, tdnfCacheDir), llb.Scratch(), llb.AsPersistentCacheDir(tdnfCacheNameAzlinux3, llb.CacheMountLocked))
}
