package distro

import (
	"context"
	"encoding/json"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

func (cfg *Config) HandleWorker(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		sOpt, err := frontend.SourceOptFromClient(ctx, client, platform)
		if err != nil {
			return nil, nil, err
		}

		p := platforms.DefaultSpec()
		if platform != nil {
			p = *platform
		}
		pc := llb.Platform(p)

		ignoreCache := frontend.IgnoreCache(client, cfg.ImageRef, cfg.ContextRef)
		st, err := cfg.Worker(sOpt, pc, ignoreCache)
		if err != nil {
			return nil, nil, err
		}

		def, err := st.Marshal(ctx, pc, ignoreCache)
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

		_, _, dt, err := client.ResolveImageConfig(ctx, cfg.ImageRef, sourceresolver.Opt{
			Platform: platform,
		})
		if err != nil {
			return nil, nil, err
		}

		var cfg dalec.DockerImageSpec
		if err := json.Unmarshal(dt, &cfg); err != nil {
			return nil, nil, err
		}
		return ref, &cfg, nil
	})
}

func (cfg *Config) Worker(sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, error) {
	opts = append(opts, dalec.ProgressGroup("Prepare worker image"))
	if cfg.ContextRef != "" {
		st, err := sOpt.GetContext(cfg.ContextRef, dalec.WithConstraints(opts...))
		if err != nil {
			return llb.Scratch(), err
		}
		if st != nil {
			return *st, nil
		}
	}

	base := frontend.GetBaseImage(sOpt, cfg.ImageRef, opts...).
		Run(
			dalec.WithConstraints(opts...),
			AptInstall(cfg.BuilderPackages, opts...),
			dalec.WithMountedAptCache(cfg.AptCachePrefix),
		).
		// This file prevents installation of things like docs in ubuntu
		// containers We don't want to exclude this because tests want to
		// check things for docs in the build container. But we also don't
		// want to remove this completely from the base worker image in the
		// frontend because we usually don't want such things in the build
		// environment. This is only needed because certain tests (which
		// are using this customized builder image) are checking for files
		// that are being excluded by this config file.
		File(llb.Rm("/etc/dpkg/dpkg.cfg.d/excludes", llb.WithAllowNotFound(true)), opts...)

	return base, nil
}
