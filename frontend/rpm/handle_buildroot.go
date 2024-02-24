package rpm

import (
	"context"
	"fmt"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/image"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

func BuildrootHandler(target string) frontend.BuildFunc {
	return func(ctx context.Context, client gwclient.Client, spec *dalec.Spec) (gwclient.Reference, *image.Image, error) {
		sOpt, err := frontend.SourceOptFromClient(ctx, client)
		if err != nil {
			return nil, nil, err
		}

		st, err := SpecToBuildrootLLB(spec, target, sOpt)
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

// SpecToBuildrootLLB converts a dalec.Spec to an rpm buildroot
func SpecToBuildrootLLB(spec *dalec.Spec, target string, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, error) {
	if err := ValidateSpec(spec); err != nil {
		return llb.Scratch(), fmt.Errorf("invalid spec: %w", err)
	}
	opts = append(opts, dalec.ProgressGroup("Create RPM buildroot"))
	sources, err := Dalec2SourcesLLB(spec, sOpt, opts...)
	if err != nil {
		return llb.Scratch(), err
	}

	return Dalec2SpecLLB(spec, dalec.MergeAtPath(llb.Scratch(), sources, "SOURCES"), target, "", opts...)
}
