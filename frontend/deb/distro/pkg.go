package distro

import (
	"context"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/frontend/deb"
	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

func (d *Config) BuildDeb(ctx context.Context, worker llb.State, sOpt dalec.SourceOpts, client gwclient.Client, spec *dalec.Spec, targetKey string, opts ...llb.ConstraintsOpt) (llb.State, error) {
	opts = append(opts, dalec.ProgressGroup("Build deb package"))

	versionID := d.VersionID
	if versionID == "" {
		var err error
		versionID, err = deb.ReadDistroVersionID(ctx, client, worker)
		if err != nil {
			return worker, err
		}
	}

	worker = worker.With(d.InstallBuildDeps(sOpt, spec, targetKey))
	srcPkg, err := deb.SourcePackage(sOpt, worker, spec, targetKey, versionID, opts...)
	if err != nil {
		return worker, err
	}

	builder := worker.With(dalec.SetBuildNetworkMode(spec))

	st, err := deb.BuildDeb(builder, spec, srcPkg, versionID, opts...)
	if err != nil {
		return llb.Scratch(), err
	}
	return frontend.MaybeSign(ctx, client, st, spec, targetKey, sOpt)
}

func (cfg *Config) HandleDeb(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		sOpt, err := frontend.SourceOptFromClient(ctx, client)
		if err != nil {
			return nil, nil, err
		}

		pg := dalec.ProgressGroup(spec.Name)

		worker, err := cfg.Worker(sOpt, pg)
		if err != nil {
			return nil, nil, err
		}

		st, err := cfg.BuildDeb(ctx, worker, sOpt, client, spec, targetKey, pg)
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

		ctr, err := cfg.BuildContainer(worker, sOpt, client, spec, targetKey, st, pg)
		if err != nil {
			return ref, nil, err
		}

		if ref, err := cfg.runTests(ctx, client, spec, sOpt, targetKey, ctr, pg); err != nil {
			cfg, _ := cfg.BuildImageConfig(ctx, client, spec, platform, targetKey)
			return ref, cfg, err
		}

		if platform == nil {
			p := platforms.DefaultSpec()
			platform = &p
		}
		return ref, &dalec.DockerImageSpec{Image: ocispecs.Image{Platform: *platform}}, nil
	})
}

func (cfg *Config) runTests(ctx context.Context, client gwclient.Client, spec *dalec.Spec, sOpt dalec.SourceOpts, targetKey string, ctr llb.State, opts ...llb.ConstraintsOpt) (gwclient.Reference, error) {
	def, err := ctr.Marshal(ctx, opts...)
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

	withTestDeps := cfg.InstallTestDeps(sOpt, targetKey, spec, opts...)
	err = frontend.RunTests(ctx, client, spec, ref, withTestDeps, targetKey)
	return ref, err
}

func (cfg *Config) HandleSourcePkg(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		sOpt, err := frontend.SourceOptFromClient(ctx, client)
		if err != nil {
			return nil, nil, err
		}

		pg := dalec.ProgressGroup(spec.Name)

		worker, err := cfg.Worker(sOpt, pg)
		if err != nil {
			return nil, nil, err
		}

		versionID, err := deb.ReadDistroVersionID(ctx, client, worker)
		if err != nil {
			return nil, nil, err
		}

		worker = worker.With(cfg.InstallBuildDeps(sOpt, spec, targetKey, pg))
		st, err := deb.SourcePackage(sOpt, worker, spec, targetKey, versionID, pg)
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
