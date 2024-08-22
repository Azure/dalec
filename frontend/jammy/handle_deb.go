package jammy

import (
	"context"
	"strings"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/frontend/deb"
	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

const JammyWorkerContextName = "dalec-jammy-worker"

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
		if platform == nil {
			p := platforms.DefaultSpec()
			platform = &p
		}
		return ref, &dalec.DockerImageSpec{Image: ocispecs.Image{Platform: *platform}}, nil
	})
}

func installPackages(opts llb.ConstraintsOpt, ls ...string) llb.StateOption {
	return func(in llb.State) llb.State {
		return in.Run(
			dalec.ShArgs("apt-get update && apt-get install -y "+strings.Join(ls, " ")),
			dalec.WithMountedAptCache(AptCachePrefix),
			opts,
		).Root()
	}
}

func buildDeb(ctx context.Context, client gwclient.Client, spec *dalec.Spec, sOpt dalec.SourceOpts, targetKey string, opts ...llb.ConstraintsOpt) (llb.State, error) {
	worker, err := workerBase(sOpt, opts...)
	if err != nil {
		return llb.Scratch(), err
	}

	installBuildDeps, err := buildDepends(worker, sOpt, spec, targetKey, opts...)
	if err != nil {
		return llb.Scratch(), errors.Wrap(err, "error creating deb for build dependencies")
	}

	worker = worker.With(installBuildDeps)
	st, err := deb.BuildDeb(worker, spec, sOpt, targetKey, opts...)
	if err != nil {
		return llb.Scratch(), err
	}

	signed, err := frontend.MaybeSign(ctx, client, st, spec, targetKey, sOpt)
	if err != nil {
		return llb.Scratch(), err
	}
	return signed, nil
}

func workerBase(sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, error) {
	base, err := sOpt.GetContext(jammyRef, dalec.WithConstraints(opts...))
	if err != nil {
		return llb.Scratch(), err
	}
	if base != nil {
		return *base, nil
	}

	base, err = sOpt.GetContext(JammyWorkerContextName, dalec.WithConstraints(opts...))
	if err != nil {
		return llb.Scratch(), err
	}

	if base != nil {
		return *base, nil
	}

	return llb.Image(jammyRef, llb.WithMetaResolver(sOpt.Resolver)).With(basePackages(opts...)), nil
}

func basePackages(opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		opts = append(opts, dalec.ProgressGroup("Install base packages"))
		return in.
			With(installPackages(dalec.WithConstraints(opts...), "dpkg-dev", "devscripts", "equivs", "fakeroot", "dh-make", "build-essential", "dh-apparmor", "dh-make", "dh-exec", "debhelper-compat="+deb.DebHelperCompat))
	}
}

func buildDepends(worker llb.State, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, opts ...llb.ConstraintsOpt) (llb.StateOption, error) {
	deps := spec.Dependencies
	if t, ok := spec.Targets[targetKey]; ok {
		if t.Dependencies != nil {
			deps = t.Dependencies
		}
	}

	var buildDeps map[string]dalec.PackageConstraints
	if deps != nil {
		buildDeps = deps.Build
	}

	if len(buildDeps) == 0 {
		return func(in llb.State) llb.State {
			return in
		}, nil
	}

	depsSpec := &dalec.Spec{
		Name:     spec.Name + "-deps",
		Packager: "Dalec",
		Version:  spec.Version,
		Revision: spec.Revision,
		Dependencies: &dalec.PackageDependencies{
			Runtime: buildDeps,
		},
		Description: "Build dependencies for " + spec.Name,
	}

	pg := dalec.ProgressGroup("Install build dependencies")
	opts = append(opts, pg)
	deb, err := deb.BuildDeb(worker, depsSpec, sOpt, targetKey, append(opts, dalec.ProgressGroup("Create intermediate deb for build dependnencies"))...)
	if err != nil {
		return nil, errors.Wrap(err, "error creating intermediate package for installing build dependencies")
	}

	return func(in llb.State) llb.State {
		return in.Run(
			llb.AddMount("/tmp/builddeps", deb, llb.Readonly),
			dalec.ShArgs("apt update && apt install -y /tmp/builddeps/*.deb"),
			dalec.WithMountedAptCache(AptCachePrefix),
			dalec.WithConstraints(opts...),
		).Root()
	}, nil
}
