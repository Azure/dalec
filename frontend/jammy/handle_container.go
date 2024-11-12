package jammy

import (
	"context"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/frontend/deb/distro"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

func handleContainer(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		sOpt, err := frontend.SourceOptFromClient(ctx, client)
		if err != nil {
			return nil, nil, err
		}

		pg := dalec.ProgressGroup("Building Jammy container: " + spec.Name)

		deb, err := distroConfig.BuildDeb(ctx, sOpt, client, spec, targetKey, pg)
		if err != nil {
			return nil, nil, err
		}

		img, err := distroConfig.BuildImageConfig(ctx, client, spec, platform, targetKey)
		if err != nil {
			return nil, nil, err
		}

		ref, err := runTests(ctx, client, spec, sOpt, deb, targetKey, pg)
		return ref, img, err
	})
}

func buildImageRootfs(worker llb.State, spec *dalec.Spec, sOpt dalec.SourceOpts, debSt llb.State, targetKey string, opts ...llb.ConstraintsOpt) (llb.State, error) {
	base := dalec.GetBaseOutputImage(spec, targetKey)

	installSymlinks := func(in llb.State) llb.State {
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

	customRepoOpts, err := distroConfig.RepoMounts(spec.GetInstallRepos(targetKey), sOpt, opts...)
	if err != nil {
		return llb.Scratch(), err
	}

	if base == "" {
		base = jammyRef
	}

	baseImg := llb.Image(base, llb.WithMetaResolver(sOpt.Resolver))

	debug := llb.Scratch().File(llb.Mkfile("debug", 0o644, []byte(`debug=2`)), opts...)
	opts = append(opts, dalec.ProgressGroup("Install spec package"))

	return baseImg.Run(
		dalec.WithConstraints(opts...),
		customRepoOpts,
		llb.AddEnv("DEBIAN_FRONTEND", "noninteractive"),
		dalec.WithMountedAptCache(AptCachePrefix),
		llb.AddMount("/etc/dpkg/dpkg.cfg.d/99-dalec-debug", debug, llb.SourcePath("debug"), llb.Readonly),
		dalec.RunOptFunc(func(cfg *llb.ExecInfo) {
			tmp := llb.Scratch().File(llb.Mkfile("tmp", 0o644, nil))
			// Warning: HACK here
			// The base ubuntu image has this `excludes` config file which prevents
			// installation of a lot of things, including doc files.
			// This is mounting over that file with an empty file so that our test suite
			// passes (as it is looking at these files).
			llb.AddMount("/etc/dpkg/dpkg.cfg.d/excludes", tmp, llb.SourcePath("tmp")).SetRunOption(cfg)
		}),
		distro.InstallLocalPkg(debSt),
	).Root().
		With(installSymlinks), nil
}

func installTestDeps(spec *dalec.Spec, sOpt dalec.SourceOpts, targetKey string, opts ...llb.ConstraintsOpt) (llb.StateOption, error) {
	deps := spec.GetTestDeps(targetKey)
	if len(deps) == 0 {
		return func(s llb.State) llb.State { return s }, nil
	}

	extraRepoOpts, err := distroConfig.RepoMounts(spec.GetTestRepos(targetKey), sOpt, opts...)
	if err != nil {
		return nil, err
	}

	return func(in llb.State) llb.State {
		opts = append(opts, dalec.ProgressGroup("Install test dependencies"))
		return in.Run(
			dalec.WithConstraints(opts...),
			distro.AptInstall(deps...),
			extraRepoOpts,
			dalec.WithMountedAptCache(AptCachePrefix),
		).Root()
	}, nil
}
