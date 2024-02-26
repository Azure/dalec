package debug

import (
	"context"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/image"
	"github.com/moby/buildkit/frontend/gateway/client"
)

// HandleSources is a handler that outputs all the sources.
func HandleSources(ctx context.Context, gwc client.Client, spec *dalec.Spec) (client.Reference, *image.Image, error) {
	sOpt, err := frontend.SourceOptFromClient(ctx, gwc)
	if err != nil {
		return nil, nil, err
	}

	sources := make([]llb.State, 0, len(spec.Sources))
	for name, src := range spec.Sources {
		st, err := dalec.Source2LLBGetter(spec, src, name, sOpt)
		if err != nil {
			return nil, nil, err
		}

		sources = append(sources, st)
	}

	def, err := dalec.MergeAtPath(llb.Scratch(), sources, "/").Marshal(ctx)
	if err != nil {
		return nil, nil, err
	}

	res, err := gwc.Solve(ctx, client.SolveRequest{
		Definition: def.ToPB(),
	})
	if err != nil {
		return nil, nil, err
	}
	ref, err := res.SingleRef()
	if err != nil {
		return nil, nil, err
	}
	return ref, &image.Image{}, nil
}
