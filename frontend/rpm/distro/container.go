package distro

import (
	"context"
	"path/filepath"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/frontend/pkg/bkfs"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/util/bklog"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

func (cfg *Config) BuildContainer(worker llb.State, spec *dalec.Spec, targetKey string, rpmDir llb.State, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, error) {
	opts = append(opts, dalec.ProgressGroup("Install RPMs"))
	const workPath = "/tmp/rootfs"

	rootfs := llb.Scratch()
	if ref := dalec.GetBaseOutputImage(spec, targetKey); ref != "" {
		rootfs = llb.Image(ref, llb.WithMetaResolver(sOpt.Resolver), dalec.WithConstraints(opts...))
	}

	installTimeRepos := spec.GetInstallRepos(targetKey)
	repoMounts, keyPaths, err := cfg.RepoMounts(installTimeRepos, sOpt, opts...)
	if err != nil {
		return llb.Scratch(), err
	}

	importRepos := []DnfInstallOpt{DnfWithMounts(repoMounts), DnfImportKeys(keyPaths)}

	rpmMountDir := "/tmp/rpms"
	pkgs := cfg.BasePackages
	pkgs = append(pkgs, filepath.Join(rpmMountDir, "**/*.rpm"))

	installOpts := []DnfInstallOpt{DnfAtRoot(workPath)}
	installOpts = append(installOpts, importRepos...)
	installOpts = append(installOpts, []DnfInstallOpt{
		DnfNoGPGCheck,
		dnfInstallWithConstraints(opts)}...)

	rootfs = worker.Run(
		cfg.Install(pkgs, installOpts...),
		llb.AddMount(rpmMountDir, rpmDir, llb.SourcePath("/RPMS")),
		dalec.WithConstraints(opts...),
	).AddMount(workPath, rootfs)

	if post := spec.GetImagePost(targetKey); post != nil && len(post.Symlinks) > 0 {
		rootfs = worker.
			Run(dalec.WithConstraints(opts...), dalec.InstallPostSymlinks(post, workPath)).
			AddMount(workPath, rootfs)
	}

	return rootfs, nil
}

func (cfg *Config) HandleContainer(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
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

		rpm, err := cfg.BuildRPM(worker, ctx, client, spec, sOpt, targetKey, pg)
		if err != nil {
			return nil, nil, err
		}

		img, err := cfg.BuildImageConfig(ctx, client, spec, platform, targetKey)
		if err != nil {
			return nil, nil, err
		}

		ctr, err := cfg.BuildContainer(worker, spec, targetKey, rpm, sOpt, pg)
		if err != nil {
			return nil, nil, err
		}

		fs, err := bkfs.FromState(ctx, &ctr, client)
		if err != nil {
			return nil, nil, err
		}

		entries, err := fs.ReadDir(".")
		if err != nil {
			return nil, nil, err
		}

		for _, entry := range entries {
			bklog.L.Warnf("entry: %s", entry.Name())
		}

		ref, err := cfg.runTests(ctx, worker, client, spec, sOpt, ctr, targetKey, pg)
		return ref, img, err
	})
}

func (cfg *Config) HandleDepsOnly(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return nil, nil
}
