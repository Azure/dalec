package jammy

import (
	"context"
	"fmt"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

const jammyRef = "ubuntu:jammy"

func handleContainer(ctx context.Context, client gwclient.Client, spec *dalec.Spec) (gwclient.Reference, *dalec.DockerImageSpec, error) {
	sOpt, err := frontend.SourceOptFromClient(ctx, client)
	if err != nil {
		return nil, nil, err
	}

	deb, err := BuildDeb(spec, sOpt)
	if err != nil {
		return nil, nil, err
	}

	st := buildImageRootfs(spec, sOpt, deb)

	def, err := st.Marshal(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("error marshalling llb: %w", err)
	}

	res, err := client.Solve(ctx, gwclient.SolveRequest{
		Definition: def.ToPB(),
	})
	if err != nil {
		return nil, nil, err
	}

	platform, err := workerImg(sOpt).GetPlatform(ctx)
	if err != nil {
		return nil, nil, err
	}

	img, err := frontend.BuildImageConfig(ctx, client, spec, targetKey, jammyRef, frontend.WithPlatform(*platform))
	if err != nil {
		return nil, nil, err
	}

	ref, err := res.SingleRef()
	if err != nil {
		return nil, nil, err
	}

	if err := frontend.RunTests(ctx, client, spec, ref, targetKey); err != nil {
		return nil, nil, err
	}

	return ref, img, err
}

func buildImageRootfs(spec *dalec.Spec, sOpt dalec.SourceOpts, deb llb.State) llb.State {
	base := dalec.GetOutputBaseImageRef(spec, targetKey)

	worker := workerImg(sOpt).
		Run(
			shArgs("apt-get update && apt-get install -y apt-utils mmdebstrap"),
		).
		Root()

	repoMount := addAptRepoForDeb(worker, deb, spec)

	if base == "" {
		return boostrapRootfs(worker, repoMount, spec)
	}

	baseImg := llb.Image(base, llb.WithMetaResolver(sOpt.Resolver))
	return buildRootfsFromBase(baseImg, repoMount, spec)
}

// buildRootfsFromBase creates a rootfs from a base image and installs the built package and all its runtime dependencies
// This requires apt-get and /bin/sh to be installed in the base image
func buildRootfsFromBase(base llb.State, repoMount llb.RunOption, spec *dalec.Spec) llb.State {
	return base.Run(
		shArgs("apt-get update && apt-get install -y "+spec.Name),
		varCacheAptMount,
		varLibAptMount,
		repoMount,
	).Root()
}

// bootstrapRootfs creates a rootfs from scratch using the built package and all its runtime dependencies
func boostrapRootfs(worker llb.State, repoMount llb.RunOption, spec *dalec.Spec) llb.State {
	return worker.
		Run(
			shArgs("apt-get update && mmdebstrap --variant=custom --mode=chrootless --include="+spec.Name+" jammy /tmp/rootfs /etc/apt/sources.list && rm -rf /tmp/rootfs/var/lib/apt && rm -rf /tmp/rootfs/var/cache/apt"),
			llb.AddEnv("DEBIAN_FRONTEND", "noninteractive"),
			repoMount,
		).
		AddMount("/tmp/rootfs", llb.Scratch())
}

func addAptRepoForDeb(base llb.State, deb llb.State, spec *dalec.Spec) llb.RunOption {
	return dalec.RunOptFunc(func(ei *llb.ExecInfo) {
		repo := base.
			Run(
				shArgs("apt-ftparchive packages . | gzip -1 > Packages.gz"),
				llb.Dir("/tmp/deb"),
			).
			AddMount("/tmp/deb", deb)

		sources := base.
			Run(
				shArgs("set -e; echo 'deb [trusted=yes] copy:/tmp/_dalec_apt_repo_deb/ /' > /tmp/sources/list; cat /etc/apt/sources.list >> /tmp/sources/list"),
			).
			AddMount("/tmp/sources", llb.Scratch())

		llb.AddMount("/tmp/_dalec_apt_repo_deb", repo, llb.Readonly).SetRunOption(ei)
		llb.AddMount("/etc/apt/sources.list", sources, llb.Readonly, llb.SourcePath("list")).SetRunOption(ei)
	})
}
