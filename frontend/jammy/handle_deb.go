package jammy

import (
	"context"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

const JammyWorkerContextName = "dalec-jammy-worker"

func handleDeb(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		sOpt, err := frontend.SourceOptFromClient(ctx, client)
		if err != nil {
			return nil, nil, err
		}

		pg := dalec.ProgressGroup("Building Jammy deb package: " + spec.Name)
		st, err := distroConfig.BuildDeb(ctx, sOpt, client, spec, targetKey, pg)
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

		ref, err := res.SingleRef()
		if err != nil {
			return nil, nil, err
		}

		if err := ref.Evaluate(ctx); err != nil {
			return ref, nil, err
		}

		if ref, err := runTests(ctx, client, spec, sOpt, st, targetKey, pg); err != nil {
			cfg, _ := distroConfig.BuildImageConfig(ctx, client, spec, platform, targetKey)
			return ref, cfg, err
		}

		if platform == nil {
			p := platforms.DefaultSpec()
			platform = &p
		}
		return ref, &dalec.DockerImageSpec{Image: ocispecs.Image{Platform: *platform}}, nil
	})
}

func runTests(ctx context.Context, client gwclient.Client, spec *dalec.Spec, sOpt dalec.SourceOpts, deb llb.State, targetKey string, opts ...llb.ConstraintsOpt) (gwclient.Reference, error) {
	worker, err := distroConfig.Worker(sOpt, opts...)
	if err != nil {
		return nil, err
	}

	st, err := buildImageRootfs(worker, spec, sOpt, deb, targetKey, opts...)
	if err != nil {
		return nil, err
	}

	def, err := st.Marshal(ctx, opts...)
	if err != nil {
		return nil, err
	}

	res, err := client.Solve(ctx, gwclient.SolveRequest{
		Definition: def.ToPB(),
	})
	if err != nil {
		return nil, err
	}

	ref, err := res.SingleRef()
	if err != nil {
		return nil, err
	}

	withTestDeps, err := installTestDeps(spec, sOpt, targetKey, opts...)
	if err != nil {
		return nil, err
	}

	err = frontend.RunTests(ctx, client, spec, ref, withTestDeps, targetKey)
	return ref, err
}
