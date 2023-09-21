package rpm

import (
	"context"
	"fmt"

	"github.com/azure/dalec"
	"github.com/azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/image"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
)

func BuildrootHandler(target string) frontend.BuildFunc {
	return func(ctx context.Context, client gwclient.Client, spec *dalec.Spec) (gwclient.Reference, *image.Image, error) {
		caps := client.BuildOpts().LLBCaps
		noMerge := !caps.Contains(pb.CapMergeOp)

		st, err := specToBuildrootLLB(spec, noMerge, client, frontend.ForwarderFromClient(ctx, client), target)
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
}

func specToBuildrootLLB(spec *dalec.Spec, noMerge bool, mr llb.ImageMetaResolver, forward dalec.ForwarderFunc, target string) (llb.State, error) {
	sources, err := Dalec2SourcesLLB(spec, mr, forward)
	if err != nil {
		return llb.Scratch(), err
	}

	return Dalec2SpecLLB(spec, mergeOrCopy(llb.Scratch(), sources, "SOURCES", noMerge), target, "")
}
