package windows

import (
	"context"
	"fmt"

	"github.com/Azure/dalec"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/image"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

const (
	workerImgRef = "mcr.microsoft.com/mirror/docker/library/buildpack-deps:bullseye"
)

var (
	varCacheAptMount = llb.AddMount("/var/cache/apt", llb.Scratch(), llb.AsPersistentCacheDir("dalec-windows-var-cache-apt", llb.CacheMountLocked))
	varLibAptMount   = llb.AddMount("/var/lib/apt", llb.Scratch(), llb.AsPersistentCacheDir("dalec-windows-var-lib-apt", llb.CacheMountLocked))
)

func handleZip(ctx context.Context, client gwclient.Client, spec *dalec.Spec) (gwclient.Reference, *image.Image, error) {
	st := llb.Scratch().File(llb.Mkfile("/hello", 0600, []byte("hello world!")))

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
	ref, err := res.SingleRef()
	// Do not return a nil image, it may cause a panic
	return ref, &image.Image{}, err
}

func workerImg(sOpt dalec.SourceOpts) llb.State {
	// TODO: support named context override... also this should possibly be its own image, maybe?
	return llb.Image(workerImgRef, llb.WithMetaResolver(sOpt.Resolver)).
		Run(
			shArgs("apt-get update && apt-get install -y build-essential binutils-mingw-w64 g++-mingw-w64-x86-64 gcc git make pkg-config quilt zip"),
			varCacheAptMount,
			varLibAptMount,
		).Root()
}

func shArgs(cmd string) llb.RunOption {
	return llb.Args([]string{"sh", "-c", cmd})
}
