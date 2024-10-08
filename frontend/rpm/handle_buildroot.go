package rpm

import (
	"context"
	"fmt"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

type WorkerFunc func(resolver llb.ImageMetaResolver, spec *dalec.Spec, targetKey string, opts ...llb.ConstraintsOpt) (llb.State, error)

func HandleBuildroot(wf WorkerFunc) gwclient.BuildFunc {
	return func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
		return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
			sOpt, err := frontend.SourceOptFromClient(ctx, client)
			if err != nil {
				return nil, nil, err
			}

			worker, err := wf(sOpt.Resolver, spec, targetKey)
			if err != nil {
				return nil, nil, err
			}

			st, err := SpecToBuildrootLLB(worker, spec, sOpt, targetKey)
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
			if err != nil {
				return nil, nil, err
			}

			return ref, &dalec.DockerImageSpec{}, nil
		})
	}
}

// SpecToBuildrootLLB converts a dalec.Spec to an rpm buildroot
func SpecToBuildrootLLB(worker llb.State, spec *dalec.Spec, sOpt dalec.SourceOpts, targetKey string, opts ...llb.ConstraintsOpt) (llb.State, error) {
	if err := ValidateSpec(spec); err != nil {
		return llb.Scratch(), fmt.Errorf("invalid spec: %w", err)
	}
	opts = append(opts, dalec.ProgressGroup("Create RPM buildroot"))

	sources, err := Dalec2SourcesLLB(worker, spec, sOpt, opts...)
	if err != nil {
		return llb.Scratch(), err
	}

	return Dalec2SpecLLB(spec, dalec.MergeAtPath(llb.Scratch(), sources, "SOURCES", opts...), targetKey, "", opts...)
}
