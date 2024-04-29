package azlinux

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/frontend/rpm"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

func handleRPM(w worker) gwclient.BuildFunc {
	return func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
		return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
			if err := rpm.ValidateSpec(spec); err != nil {
				return nil, nil, fmt.Errorf("rpm: invalid spec: %w", err)
			}

			pg := dalec.ProgressGroup("Building " + targetKey + " rpm: " + spec.Name)
			sOpt, err := frontend.SourceOptFromClient(ctx, client)
			if err != nil {
				return nil, nil, err
			}

			st, err := specToRpmLLB(w, client, spec, sOpt, targetKey, pg)
			if err != nil {
				return nil, nil, err
			}

			if signer, ok := spec.GetSigner(targetKey); ok {
				signed, err := frontend.ForwardToSigner(ctx, client, platform, signer, st)
				if err != nil {
					return nil, nil, err
				}
				st = signed
			}

			def, err := st.Marshal(ctx, pg)
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
			return ref, nil, nil
		})
	}
}

func shArgs(cmd string) llb.RunOption {
	return llb.Args([]string{"sh", "-c", cmd})
}

func installBuildDeps(w worker, spec *dalec.Spec, targetKey string, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		deps := spec.GetBuildDeps(targetKey)
		if len(deps) == 0 {
			return in
		}
		opts = append(opts, dalec.ProgressGroup("Install build deps"))

		return in.Run(w.Install(deps, installWithConstraints(opts)), dalec.WithConstraints(opts...)).Root()
	}
}

func specToRpmLLB(w worker, client gwclient.Client, spec *dalec.Spec, sOpt dalec.SourceOpts, targetKey string, opts ...llb.ConstraintsOpt) (llb.State, error) {
	base := w.Base(client, opts...).With(installBuildDeps(w, spec, targetKey, opts...))
	br, err := rpm.SpecToBuildrootLLB(base, spec, sOpt, targetKey, opts...)
	if err != nil {
		return llb.Scratch(), err
	}
	specPath := filepath.Join("SPECS", spec.Name, spec.Name+".spec")
	return rpm.Build(br, base, specPath, opts...), nil
}
