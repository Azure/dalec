package jammy

import (
	"context"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/frontend/deb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

func handleDebianSourcePackage(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		sOpt, err := frontend.SourceOptFromClient(ctx, client)
		if err != nil {
			return nil, nil, err
		}

		opt := dalec.ProgressGroup("Building Jammy source package: " + spec.Name)
		worker, err := workerBase(sOpt, opt)
		if err != nil {
			return nil, nil, err
		}
		installBuildDeps, err := buildDepends(worker, sOpt, spec, targetKey, opt)
		worker = worker.With(installBuildDeps)
		st, err := deb.SourcePackage(sOpt, worker, spec, targetKey, opt)
		if err != nil {
			return nil, nil, errors.Wrap(err, "error building source package")
		}

		def, err := st.Marshal(ctx)
		if err != nil {
			return nil, nil, errors.Wrap(err, "error marshalling source package state")
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
