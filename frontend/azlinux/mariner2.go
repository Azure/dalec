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
		w.Install([]string{"rpm-build", "mariner-rpm-macros", "build-essential", "ca-certificates"}, installWithConstraints(opts)),
		dalec.WithConstraints(opts...),
	).Root(), nil
}

func (w mariner2) InstallWithReqs(pkgs []string, reqs [][]string, opts ...installOpt) installFunc {
	buildDeps := map[string][]string{}
	for i := range pkgs {
		buildDeps[pkgs[i]] = reqs[i]
	}

	// depsOnly is a simple dalec spec that only includes build dependencies and their constraints
	depsOnly := dalec.Spec{
		Name:        "mariner2 build dependencies",
		Description: "Build dependencies for mariner2",
		Version:     "0.1",
		Dependencies: &dalec.PackageDependencies{
			Build: buildDeps,
		},
	}

	return func(ctx context.Context, client gwclient.Client, sOpt dalec.SourceOpts) (llb.RunOption, error) {
		pg := dalec.ProgressGroup("Building container for build dependencies")

		// create an RPM with just the build dependencies, using our same base worker
		rpmDir, err := specToRpmLLB(ctx, w, client, &depsOnly, sOpt, "mariner2", pg)
		if err != nil {
			return nil, err
		}

		// read the built RPMs (there should be a single one)
		rpms, err := readRPMs(ctx, client, rpmDir)
		if err != nil {
			return nil, err
		}

		var opts []llb.ConstraintsOpt
		opts = append(opts, dalec.ProgressGroup("Install build deps"))

		rpmMountDir := "/tmp/rpms"
		fullRPMPaths := make([]string, 0, len(rpms))
		for _, rpm := range rpms {
			fullRPMPaths = append(fullRPMPaths, filepath.Join(rpmMountDir, rpm))
		}

		// install the RPM into the worker itself, using the same base worker
		return w.Install(fullRPMPaths, noGPGCheck, installWithConstraints(opts),
			withMounts(llb.AddMount(rpmMountDir, rpmDir, llb.SourcePath("/RPMS")))), nil
	}
}

func (w mariner2) Install(pkgs []string, opts ...installOpt) llb.RunOption {
	var cfg installConfig
	setInstallOptions(&cfg, opts)
	return dalec.WithRunOptions(tdnfInstall(&cfg, "2.0", pkgs), w.tdnfCacheMount(cfg.root))
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
