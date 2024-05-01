package jammy

import (
	"context"
	"strings"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/frontend/deb"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

func handleDeb(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		sOpt, err := frontend.SourceOptFromClient(ctx, client)
		if err != nil {
			return nil, nil, err
		}

		st, err := buildDeb(ctx, client, spec, sOpt, targetKey, dalec.ProgressGroup("Building Jammy deb package: "+spec.Name))
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
		return ref, nil, nil
	})
}

func installPackages(opts llb.ConstraintsOpt, ls ...string) llb.StateOption {
	return func(in llb.State) llb.State {
		return in.Run(
			dalec.ShArgs("apt-get update && apt-get install -y "+strings.Join(ls, " ")),
			dalec.WithMountedAptCache(aptCachePrefix),
			opts,
		).Root()
	}
}

func buildDeb(ctx context.Context, client gwclient.Client, spec *dalec.Spec, sOpt dalec.SourceOpts, targetKey string, opts ...llb.ConstraintsOpt) (llb.State, error) {
	worker := workerBase(sOpt.Resolver).With(basePackages(opts...)).With(buildDepends(spec, targetKey, opts...))
	st, err := deb.BuildDeb(worker, spec, sOpt, targetKey, opts...)
	if err != nil {
		return llb.Scratch(), err
	}

	signed, err := frontend.MaybeSign(ctx, client, st, spec, targetKey)
	if err != nil {
		return llb.Scratch(), err
	}
	return signed, nil
}

func workerBase(resolver llb.ImageMetaResolver) llb.State {
	return llb.Image(jammyRef, llb.WithMetaResolver(resolver))
}

func basePackages(opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		opts = append(opts, dalec.ProgressGroup("Install base packages"))
		return in.
			With(installPackages(dalec.WithConstraints(opts...), "dpkg-dev", "devscripts", "equivs", "fakeroot", "dh-make", "build-essential", "dh-apparmor", "dh-make", "dh-exec"))
	}
}

func buildDepends(spec *dalec.Spec, targetKey string, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		opts = append(opts, dalec.ProgressGroup("Install build dependencies"))
		deps := spec.GetBuildDeps(targetKey)
		return in.With(installPackages(dalec.WithConstraints(opts...), deps...))
	}
}
