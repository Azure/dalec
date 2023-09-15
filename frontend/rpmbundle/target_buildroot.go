package rpmbundle

import (
	"context"
	"fmt"

	"github.com/azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/image"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
)

func handleBuildRoot(ctx context.Context, client gwclient.Client, spec *frontend.Spec) (gwclient.Reference, *image.Image, error) {
	caps := client.BuildOpts().LLBCaps
	noMerge := !caps.Contains(pb.CapMergeOp)

	st, err := specToBuildrootLLB(spec, noMerge, client)
	if err != nil {
		return nil, nil, err
	}

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
	return ref, nil, err
}

func specToBuildrootLLB(spec *frontend.Spec, noMerge bool, mr llb.ImageMetaResolver) (llb.State, error) {
	sources, err := specToSourcesLLB(spec, mr)
	if err != nil {
		return llb.Scratch(), err
	}

	return specToRpmSpecLLB(spec, mergeOrCopy(llb.Scratch(), sources, "SOURCES", noMerge), "")
}
