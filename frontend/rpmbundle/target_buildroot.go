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
	cf := client.(reexecFrontend)
	localSt, err := cf.CurrentFrontend()
	if err != nil {
		return nil, nil, fmt.Errorf("could not get current frontend: %w", err)
	}
	caps := client.BuildOpts().LLBCaps
	noMerge := !caps.Contains(pb.CapMergeOp)

	st, err := specToBuildrootLLB(spec, localSt, noMerge)
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

func specToBuildrootLLB(spec *frontend.Spec, localSt *llb.State, noMerge bool) (llb.State, error) {
	out, err := specToSourcesLLB(spec, localSt, noMerge, llb.Scratch(), "SOURCES")
	if err != nil {
		return llb.Scratch(), err
	}
	return specToRpmSpecLLB(spec, out)
}
