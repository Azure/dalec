package jammy

import (
	"context"
	"io/fs"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/frontend/deb"
	"github.com/Azure/dalec/frontend/pkg/bkfs"
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

		opt := dalec.ProgressGroup("Building Jammy deb package: " + spec.Name)
		st, err := buildDeb(ctx, client, spec, sOpt, targetKey, opt)
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

		if ref, err := runTests(ctx, client, spec, sOpt, st, targetKey, opt); err != nil {
			cfg, _ := buildImageConfig(ctx, client, spec, platform, targetKey)
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
	worker, err := workerBase(sOpt, opts...)
	if err != nil {
		return nil, err
	}

	var includeTestRepo bool

	workerFS, err := bkfs.FromState(ctx, &worker, client)
	if err != nil {
		return nil, err
	}

	// Check if there there is a test repo in the worker image.
	// We'll mount that into the target container while installing packages.
	_, repoErr := fs.Stat(workerFS, testRepoPath[1:])
	_, listErr := fs.Stat(workerFS, testRepoSourceListPath[1:])
	if listErr == nil && repoErr == nil {
		// This is a test and we need to include the repo from the worker image
		// into target container.
		includeTestRepo = true
		frontend.Warn(ctx, client, worker, "Including test repo from worker image")
	}

	st, err := buildImageRootfs(worker, spec, sOpt, deb, targetKey, includeTestRepo, opts...)
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

var jammyRepoPlatform = dalec.RepoPlatformConfig{
	ConfigRoot: "/etc/apt/sources.list.d",
	GPGKeyRoot: "/usr/share/keyrings",
	ConfigExt:  ".list",
}

func customRepoMounts(repos []dalec.PackageRepositoryConfig, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) (llb.RunOption, error) {
	withRepos, err := dalec.WithRepoConfigs(repos, &jammyRepoPlatform, sOpt, opts...)
	if err != nil {
		return nil, err
	}

	withData, err := dalec.WithRepoData(repos, sOpt, opts...)
	if err != nil {
		return nil, err
	}

	keyMounts, _, err := dalec.GetRepoKeys(repos, &jammyRepoPlatform, sOpt, opts...)
	if err != nil {
		return nil, err
	}

	return dalec.WithRunOptions(withRepos, withData, keyMounts), nil
}

func buildDeb(ctx context.Context, client gwclient.Client, spec *dalec.Spec, sOpt dalec.SourceOpts, targetKey string, opts ...llb.ConstraintsOpt) (llb.State, error) {
	worker, err := workerBase(sOpt, opts...)
	if err != nil {
		return llb.Scratch(), err
	}

	versionID, err := deb.ReadDistroVersionID(ctx, client, worker)
	if err != nil {
		return llb.Scratch(), err
	}

	installBuildDeps, err := buildDepends(worker, sOpt, spec, targetKey, opts...)
	if err != nil {
		return llb.Scratch(), errors.Wrap(err, "error creating deb for build dependencies")
	}

	builder := worker.With(installBuildDeps)
	srcPkg, err := deb.SourcePackage(sOpt, builder, spec, targetKey, versionID, opts...)
	if err != nil {
		return llb.Scratch(), err
	}

	builder = builder.With(dalec.SetBuildNetworkMode(spec))
	st, err := deb.BuildDeb(builder, spec, srcPkg, versionID, opts...)
	if err != nil {
		return llb.Scratch(), err
	}

	signed, err := frontend.MaybeSign(ctx, client, st, spec, targetKey, sOpt, opts...)
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

	return llb.Image(jammyRef, llb.WithMetaResolver(sOpt.Resolver)).With(basePackages(opts...)).
		// This file prevents installation of things like docs in ubuntu
		// containers We don't want to exclude this because tests want to
		// check things for docs in the build container. But we also don't
		// want to remove this completely from the base worker image in the
		// frontend because we usually don't want such things in the build
		// environment. This is only needed because certain tests (which
		// are using this customized builder image) are checking for files
		// that are being excluded by this config file.
		File(llb.Rm("/etc/dpkg/dpkg.cfg.d/excludes", llb.WithAllowNotFound(true))), nil
}

func basePackages(opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		opts = append(opts, dalec.ProgressGroup("Install base packages"))
		return in.Run(
			dalec.WithConstraints(opts...),
			dalec.WithMountedAptCache(AptCachePrefix),
			deb.AptInstall(
				"aptitude",
				"dpkg-dev",
				"devscripts",
				"equivs",
				"fakeroot",
				"dh-make",
				"build-essential",
				"dh-apparmor",
				"dh-make",
				"dh-exec",
				"debhelper-compat="+deb.DebHelperCompat,
			),
		).Root()
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
		Name:     spec.Name + "-build-deps",
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

	srcPkg, err := deb.SourcePackage(sOpt, worker, depsSpec, targetKey, "", opts...)
	if err != nil {
		return nil, err
	}

	pkg, err := deb.BuildDeb(worker, depsSpec, srcPkg, "", append(opts, dalec.ProgressGroup("Create intermediate deb for build dependnencies"))...)
	if err != nil {
		return nil, errors.Wrap(err, "error creating intermediate package for installing build dependencies")
	}

	customRepoOpts, err := customRepoMounts(spec.GetBuildRepos(targetKey), sOpt, opts...)
	if err != nil {
		return nil, err
	}

	return func(in llb.State) llb.State {
		return in.Run(
			customRepoOpts,
			dalec.WithConstraints(opts...),
			deb.InstallLocalPkg(pkg),
			dalec.WithMountedAptCache(AptCachePrefix),
		).Root()
	}, nil
}
