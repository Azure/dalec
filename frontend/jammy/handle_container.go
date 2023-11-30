package jammy

import (
	"context"
	"fmt"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/containerd/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

func handleContainer(ctx context.Context, client frontend.Client, spec *dalec.Spec) (frontend.Reference, *frontend.Image, error) {
	sOpt, err := frontend.SourceOptFromClient(ctx, client)
	if err != nil {
		return nil, nil, err
	}

	deb, err := BuildDeb(spec, sOpt)
	if err != nil {
		return nil, nil, err
	}

	if spec.Image != nil && spec.Image.Base != "" {
		return nil, nil, errors.Errorf("custom base images are not supported")
	}

	worker := workerImg(sOpt).
		Run(
			shArgs("apt-get update && apt-get install -y apt-utils mmdebstrap"),
			varCacheAptMount,
			varLibAptMount,
		).Root()

	work := worker.
		Run(
			shArgs("apt-get update && mmdebstrap --variant=custom --mode=chrootless --include="+spec.Name+" jammy /tmp/rootfs /etc/apt/sources.list && rm -rf /tmp/rootfs/var/lib/apt && rm -rf /tmp/rootfs/var/cache/apt"),
			llb.AddEnv("DEBIAN_FRONTEND", "noninteractive"),
			addAptRepoForDeb(worker, deb, spec),
		)

	st := work.AddMount("/tmp/rootfs", llb.Scratch())

	def, err := st.Marshal(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("error marshalling llb: %w", err)
	}

	res, err := client.Solve(ctx, frontend.SolveRequest{
		Definition: def.ToPB(),
	})
	if err != nil {
		return nil, nil, err
	}

	platform, err := worker.GetPlatform(ctx)
	if err != nil {
		return nil, nil, err
	}
	img, err := buildImageConfig(spec, targetKey, platform)
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
				shArgs("set -e; echo 'deb [trusted=yes] file:/tmp/_dalec_apt_repo_deb/ /' > /tmp/sources/list; cat /etc/apt/sources.list >> /tmp/sources/list"),
			).
			AddMount("/tmp/sources", llb.Scratch())

		llb.AddMount("/tmp/_dalec_apt_repo_deb", repo, llb.Readonly).SetRunOption(ei)
		llb.AddMount("/etc/apt/sources.list", sources, llb.Readonly, llb.SourcePath("list")).SetRunOption(ei)
	})
}

func buildImageConfig(spec *dalec.Spec, target string, platform *v1.Platform) (*frontend.Image, error) {
	var img frontend.Image

	if platform != nil {
		img.Platform = *platform
	} else {
		img.Platform = platforms.DefaultSpec()
	}

	if err := frontend.CopyImageConfig(&img, dalec.MergeSpecImage(spec, targetKey)); err != nil {
		return nil, err
	}

	return &img, nil
}
