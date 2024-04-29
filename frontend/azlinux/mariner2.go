package azlinux

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/Azure/dalec"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

const (
	Mariner2TargetKey     = "mariner2"
	tdnfCacheNameMariner2 = "mariner2-tdnf-cache"

	mariner2Ref           = "mcr.microsoft.com/cbl-mariner/base/core:2.0"
	mariner2DistrolessRef = "mcr.microsoft.com/cbl-mariner/distroless/base:2.0"
)

func NewMariner2Handler() gwclient.BuildFunc {
	return newHandler(mariner2{})
}

type mariner2 struct{}

func (w mariner2) Base(resolver llb.ImageMetaResolver, opts ...llb.ConstraintsOpt) llb.State {
	return llb.Image(mariner2Ref, llb.WithMetaResolver(resolver), dalec.WithConstraints(opts...)).Run(
		w.Install([]string{"rpm-build", "mariner-rpm-macros", "build-essential", "ca-certificates"}, installWithConstraints(opts)),
		dalec.WithConstraints(opts...),
	).Root()
}

func (w mariner2) Install(pkgs []string, opts ...installOpt) llb.RunOption {
	var cfg installConfig
	setInstallOptions(&cfg, opts)
	return dalec.WithRunOptions(tdnfInstall(&cfg, "2.0", pkgs), w.tdnfCacheMount(cfg.root))
}

func (mariner2) DefaultImageConfig(ctx context.Context, client gwclient.Client) (*dalec.DockerImageSpec, error) {
	_, _, dt, err := client.ResolveImageConfig(ctx, mariner2DistrolessRef, sourceresolver.Opt{})
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
