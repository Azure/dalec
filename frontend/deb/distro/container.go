package distro

import (
	"context"
	"fmt"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

func (c *Config) BuildContainer(worker llb.State, sOpt dalec.SourceOpts, client gwclient.Client, spec *dalec.Spec, targetKey string, debSt llb.State, opts ...llb.ConstraintsOpt) (llb.State, error) {
	base := dalec.GetBaseOutputImage(spec, targetKey)
	if base == "" {
		base = c.DefaultOutputImage
	}

	if base == "" {
		return llb.Scratch(), fmt.Errorf("no output image ref specified, cannot build from scratch")
	}

	opts = append(opts, dalec.ProgressGroup("Build Container Image"))

	withRepos, err := c.RepoMounts(spec.GetInstallRepos(targetKey), sOpt, opts...)
	if err != nil {
		return llb.Scratch(), err
	}

	baseImg := llb.Image(base, llb.WithMetaResolver(sOpt.Resolver), dalec.WithConstraints(opts...))

	debug := llb.Scratch().File(llb.Mkfile("debug", 0o644, []byte(`debug=2`)), opts...)
	opts = append(opts, dalec.ProgressGroup("Install spec package"))

	return baseImg.Run(
		dalec.WithConstraints(opts...),
		withRepos,
		llb.AddEnv("DEBIAN_FRONTEND", "noninteractive"),
		dalec.WithMountedAptCache(c.AptCachePrefix),
		llb.AddMount("/etc/dpkg/dpkg.cfg.d/99-dalec-debug", debug, llb.SourcePath("debug"), llb.Readonly),
		dalec.RunOptFunc(func(cfg *llb.ExecInfo) {
			tmp := llb.Scratch().File(llb.Mkfile("tmp", 0o644, nil), opts...)
			// Warning: HACK here
			// The base ubuntu image has this `excludes` config file which prevents
			// installation of a lot of thigns, including doc files.
			// This is mounting over that file with an empty file so that our test suite
			// passes (as it is looking at these files).
			llb.AddMount("/etc/dpkg/dpkg.cfg.d/excludes", tmp, llb.SourcePath("tmp")).SetRunOption(cfg)
		}),
		InstallLocalPkg(debSt),
	).Root().
		With(c.createSymlinks(worker, spec, targetKey, opts...)), nil
}

func (c *Config) createSymlinks(worker llb.State, spec *dalec.Spec, targetKey string, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		post := spec.GetImagePost(targetKey)
		if post == nil {
			return in
		}

		if len(post.Symlinks) == 0 {
			return in
		}

		const workPath = "/tmp/rootfs"
		return worker.
			Run(dalec.WithConstraints(opts...), dalec.InstallPostSymlinks(post, workPath)).
			AddMount(workPath, in)
	}
}

func (c *Config) HandleContainer(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		sOpt, err := frontend.SourceOptFromClient(ctx, client)
		if err != nil {
			return nil, nil, err
		}

		pg := dalec.ProgressGroup(spec.Name)
		worker, err := c.Worker(sOpt, pg)
		if err != nil {
			return nil, nil, err
		}

		deb, err := c.BuildDeb(ctx, worker, sOpt, client, spec, targetKey, pg)
		if err != nil {
			return nil, nil, err
		}

		img, err := c.BuildImageConfig(ctx, client, spec, platform, targetKey)
		if err != nil {
			return nil, nil, err
		}

		ctr, err := c.BuildContainer(worker, sOpt, client, spec, targetKey, deb)
		if err != nil {
			return nil, nil, err
		}

		ref, err := c.runTests(ctx, client, spec, sOpt, targetKey, ctr, pg)
		return ref, img, err
	})
}
