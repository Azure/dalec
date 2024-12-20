package azlinux

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/frontend/rpm/distro"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	AzLinux3TargetKey     = "azlinux3"
	tdnfCacheNameAzlinux3 = "azlinux3-tdnf-cache"

	// Azlinux3Ref is the image ref used for the base worker image
	Azlinux3Ref      = "mcr.microsoft.com/azurelinux/base/core:3.0"
	AzLinux3FullName = "Azure Linux 3"
	// Azlinux3WorkerContextName is the build context name that can be used to lookup
	Azlinux3WorkerContextName = "dalec-azlinux3-worker"
	azlinux3DistrolessRef     = "mcr.microsoft.com/azurelinux/distroless/base:3.0"
)

var Azlinux3Config = &distro.Config{
	ImageRef:   Azlinux3Ref,
	ContextRef: Azlinux3WorkerContextName,

	ReleaseVer:         "3.0",
	BuilderPackages:    basePackages,
	BasePackages:       []string{"distroless-packages-minimal", "prebuilt-ca-certificates"},
	RepoPlatformConfig: &defaultAzlinuxRepoPlatform,
	InstallFunc:        distro.TdnfInstall,
}

func NewAzlinux3Handler() gwclient.BuildFunc {
	return newHandler(azlinux3{})
}

type azlinux3 struct{}

func (w azlinux3) Base(sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, error) {
	worker, err := sOpt.GetContext(Azlinux3WorkerContextName, dalec.WithConstraints(opts...))
	if err != nil {
		return llb.Scratch(), err
	}
	if worker != nil {
		return *worker, nil
	}

	st := frontend.GetBaseImage(sOpt, Azlinux3Ref)
	return st.Run(
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
	return []string{"distroless-packages-minimal", "prebuilt-ca-certificates"}
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

func (azlinux3) tdnfCacheMount(root string) llb.RunOption {
	return llb.AddMount(filepath.Join(root, tdnfCacheDir), llb.Scratch(), llb.AsPersistentCacheDir(tdnfCacheNameAzlinux3, llb.CacheMountLocked))
}

func (azlinux3) FullName() string {
	return AzLinux3FullName
}
