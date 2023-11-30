package mariner2

import (
	"context"
	"slices"
	"strings"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/image"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"golang.org/x/exp/maps"
)

func handleDepsOnly(ctx context.Context, client gwclient.Client, spec *dalec.Spec) (gwclient.Reference, *image.Image, error) {
	baseImg, err := getBaseBuilderImg(ctx, client)
	if err != nil {
		return nil, nil, err
	}

	sOpt, err := frontend.SourceOptFromClient(ctx, client)
	if err != nil {
		return nil, nil, err
	}

	depsSpec := dalec.GetDeps(spec, targetKey)
	deps := maps.Keys(depsSpec.Runtime)
	slices.Sort(deps)

	rpmDir := baseImg.Run(
		shArgs(`set -ex; dir="/tmp/rpms/RPMS/$(uname -m)"; mkdir -p "${dir}"; tdnf install -y --releasever=2.0 --downloadonly --alldeps --downloaddir "${dir}" `+strings.Join(deps, " ")),
		marinerTdnfCache,
	).
		AddMount("/tmp/rpms", llb.Scratch())

	st, err := specToContainerLLB(spec, targetKey, baseImg, rpmDir, sOpt)
	if err != nil {
		return nil, nil, err
	}

	def, err := st.Marshal(ctx)
	if err != nil {
		return nil, nil, err
	}

	res, err := client.Solve(ctx, gwclient.SolveRequest{
		Definition: def.ToPB(),
	})
	if err != nil {
		return nil, nil, err
	}

	img, err := buildImageConfig(ctx, spec, targetKey, client)
	if err != nil {
		return nil, nil, err
	}

	ref, err := res.SingleRef()
	if err != nil {
		return nil, nil, err
	}

	return ref, img, nil
}
