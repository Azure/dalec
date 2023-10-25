package mariner2

import (
	"context"
	"sort"
	"strings"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/image"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
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

	rpmDir := baseImg.Run(
		shArgs(`set -ex; dir="/tmp/rpms/RPMS/$(uname -m)"; mkdir -p "${dir}"; tdnf install -y --releasever=2.0 --downloadonly --alldeps --downloaddir "${dir}" `+strings.Join(getRuntimeDeps(spec), " ")),
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

func getRuntimeDeps(spec *dalec.Spec) []string {
	var deps *dalec.PackageDependencies
	if t, ok := spec.Targets[targetKey]; ok {
		deps = t.Dependencies
	}

	if deps == nil {
		deps = spec.Dependencies
		if deps == nil {
			return nil
		}
	}

	var out []string
	for p := range deps.Runtime {
		out = append(out, p)
	}

	sort.Strings(out)
	return out
}
