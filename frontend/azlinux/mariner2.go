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
	Mariner2TargetKey     = "mariner2"
	tdnfCacheNameMariner2 = "mariner2-tdnf-cache"

	Mariner2Ref               = "mcr.microsoft.com/cbl-mariner/base/core:2.0"
	Mariner2WorkerContextName = "dalec-mariner2-worker"
	mariner2DistrolessRef     = "mcr.microsoft.com/cbl-mariner/distroless/base:2.0"
)

func NewMariner2Handler() gwclient.BuildFunc {
	return newHandler(mariner2{})
}

type mariner2 struct{}

func (w mariner2) Base(sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, error) {
	base, err := sOpt.GetContext(Mariner2Ref, dalec.WithConstraints(opts...))
	if err != nil {
		return llb.Scratch(), err
	}

	if base == nil {
		base, err = sOpt.GetContext(Mariner2WorkerContextName, dalec.WithConstraints(opts...))
		if err != nil {
			return llb.Scratch(), nil
		}
	}

	if base == nil {
		st := llb.Image(Mariner2Ref, llb.WithMetaResolver(sOpt.Resolver), dalec.WithConstraints(opts...))
		base = &st
	}

	return base.Run(
		w.Install([]string{"rpm-build", "mariner-rpm-macros", "build-essential", "ca-certificates"}, dalec.NoOption(), installWithConstraints(opts)),
		dalec.WithConstraints(opts...),
	).Root(), nil
}

func (w mariner2) Install(pkgs []string, repoOpts llb.RunOption, opts ...installOpt) llb.RunOption {
	var cfg installConfig
	setInstallOptions(&cfg, opts)
	return dalec.WithRunOptions(repoOpts, tdnfInstall(&cfg, "2.0", pkgs), w.tdnfCacheMount(cfg.root))
}

func (w mariner2) BasePackages() []string {
	return []string{"mariner-release"}
}

func (mariner2) DefaultImageConfig(ctx context.Context, resolver llb.ImageMetaResolver, platform *ocispecs.Platform) (*dalec.DockerImageSpec, error) {
	_, _, dt, err := resolver.ResolveImageConfig(ctx, mariner2DistrolessRef, sourceresolver.Opt{
		Platform: platform,
	})
	if err != nil {
		return nil, err
	}

	var cfg dalec.DockerImageSpec
	if err := json.Unmarshal(dt, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (mariner2) WorkerImageConfig(ctx context.Context, resolver llb.ImageMetaResolver, platform *ocispecs.Platform) (*dalec.DockerImageSpec, error) {
	_, _, dt, err := resolver.ResolveImageConfig(ctx, Mariner2Ref, sourceresolver.Opt{Platform: platform})
	if err != nil {
		return nil, err
	}

	var cfg dalec.DockerImageSpec
	if err := json.Unmarshal(dt, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (mariner2) tdnfCacheMount(root string) llb.RunOption {
	return llb.AddMount(filepath.Join(root, tdnfCacheDir), llb.Scratch(), llb.AsPersistentCacheDir(tdnfCacheNameMariner2, llb.CacheMountLocked))
}
