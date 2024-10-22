package jammy

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

const (
	jammyRef = "mcr.microsoft.com/mirror/docker/library/ubuntu:jammy"

	testRepoPath           = "/opt/repo"
	testRepoSourceListPath = "/etc/apt/sources.list.d/test-dalec-local-repo.list"
)

func handleContainer(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		sOpt, err := frontend.SourceOptFromClient(ctx, client)
		if err != nil {
			return nil, nil, err
		}

		opt := dalec.ProgressGroup("Building Jammy container: " + spec.Name)

		deb, err := buildDeb(ctx, client, spec, sOpt, targetKey, opt)
		if err != nil {
			return nil, nil, err
		}

		img, err := buildImageConfig(ctx, client, spec, platform, targetKey)
		if err != nil {
			return nil, nil, err
		}

		ref, err := runTests(ctx, client, spec, sOpt, deb, targetKey, opt)
		return ref, img, err
	})
}

func buildImageConfig(ctx context.Context, resolver llb.ImageMetaResolver, spec *dalec.Spec, platform *ocispecs.Platform, targetKey string) (*dalec.DockerImageSpec, error) {
	ref := dalec.GetBaseOutputImage(spec, targetKey)
	if ref == "" {
		ref = jammyRef
	}

	_, _, dt, err := resolver.ResolveImageConfig(ctx, ref, sourceresolver.Opt{
		Platform: platform,
	})
	if err != nil {
		return nil, err
	}

	var i dalec.DockerImageSpec
	if err := json.Unmarshal(dt, &i); err != nil {
		return nil, errors.Wrap(err, "error unmarshalling base image config")
	}
	img := &i

	if err := dalec.BuildImageConfig(spec, targetKey, img); err != nil {
		return nil, err
	}

	return img, nil
}

func buildImageRootfs(worker llb.State, spec *dalec.Spec, sOpt dalec.SourceOpts, deb llb.State, targetKey string, includeTestRepo bool, opts ...llb.ConstraintsOpt) (llb.State, error) {
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

	customRepoOpts, err := customRepoMounts(worker, spec.GetInstallRepos(targetKey), sOpt, opts...)
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
		dalec.ShArgs("set -x; apt update && apt install -y /tmp/pkg/*.deb"),
		customRepoOpts,
		llb.AddEnv("DEBIAN_FRONTEND", "noninteractive"),
		llb.AddMount("/tmp/pkg", deb, llb.Readonly),
		dalec.WithMountedAptCache(AptCachePrefix),
		dalec.RunOptFunc(func(cfg *llb.ExecInfo) {
			if includeTestRepo {
				llb.AddMount(testRepoPath, worker, llb.SourcePath(testRepoPath)).SetRunOption(cfg)
				llb.AddMount(testRepoSourceListPath, worker, llb.SourcePath(testRepoSourceListPath)).SetRunOption(cfg)
			}
		}),
		dalec.WithConstraints(opts...),
		llb.AddMount("/etc/dpkg/dpkg.cfg.d/99-dalec-debug", debug, llb.SourcePath("debug"), llb.Readonly),
		dalec.RunOptFunc(func(cfg *llb.ExecInfo) {
			tmp := llb.Scratch().File(llb.Mkfile("tmp", 0o644, nil))
			// Warning: HACK here
			// The base ubuntu image has this `excludes` config file which prevents
			// installation of a lot of thigns, including doc files.
			// This is mounting over that file with an empty file so that our test suite
			// passes (as it is looking at these files).
			llb.AddMount("/etc/dpkg/dpkg.cfg.d/excludes", tmp, llb.SourcePath("tmp")).SetRunOption(cfg)
		}),
	).Root().
		With(installSymlinks), nil
}

func installTestDeps(worker llb.State, spec *dalec.Spec, sOpt dalec.SourceOpts, targetKey string, opts ...llb.ConstraintsOpt) (llb.StateOption, error) {
	deps := spec.GetTestDeps(targetKey)
	if len(deps) == 0 {
		return func(s llb.State) llb.State { return s }, nil
	}

	extraRepoOpts, err := customRepoMounts(worker, spec.GetTestRepos(targetKey), sOpt, opts...)
	if err != nil {
		return nil, err
	}

	return func(in llb.State) llb.State {
		opts = append(opts, dalec.ProgressGroup("Install test dependencies"))
		return in.Run(
			dalec.ShArgs("apt-get update && apt-get install -y --no-install-recommends "+strings.Join(deps, " ")),
			llb.AddEnv("DEBIAN_FRONTEND", "noninteractive"),
			extraRepoOpts,
			dalec.WithMountedAptCache(AptCachePrefix),
		).Root()
	}, nil
}
