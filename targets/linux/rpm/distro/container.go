package distro

import (
	"context"
	"path/filepath"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/targets/linux"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

func (cfg *Config) BuildContainer(_ gwclient.Client, worker llb.State, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, rpmDir llb.State, opts ...llb.ConstraintsOpt) (llb.State, error) {
	opts = append(opts, dalec.ProgressGroup("Install RPMs"))
	const workPath = "/tmp/rootfs"

	bi, err := spec.GetSingleBase(targetKey)
	if err != nil {
		return llb.Scratch(), err
	}
	rootfs, err := bi.ToState(sOpt, opts...)
	if err != nil {
		return llb.Scratch(), err
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

func (cfg *Config) HandleDepsOnly(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		pg := dalec.ProgressGroup("Build " + targetKey + " deps-only container for: " + spec.Name)

		sOpt, err := frontend.SourceOptFromClient(ctx, client)
		if err != nil {
			return nil, nil, err
		}

		worker, err := cfg.Worker(sOpt, pg)
		if err != nil {
			return nil, nil, err
		}

		deps := spec.GetRuntimeDeps(targetKey)
		var rpmDir = llb.Scratch()
		if len(deps) > 0 {
			withDownloads := worker.Run(dalec.ShArgs("set -ex; mkdir -p /tmp/rpms/RPMS/$(uname -m)")).
				Run(cfg.Install(spec.GetRuntimeDeps(targetKey),
					DnfDownloadAllDeps("/tmp/rpms/RPMS/$(uname -m)"))).Root()
			rpmDir = llb.Scratch().File(llb.Copy(withDownloads, "/tmp/rpms", "/", dalec.WithDirContentsOnly()))

		}

		ctr, err := cfg.BuildContainer(client, worker, sOpt, spec, targetKey, rpmDir, pg)
		if err != nil {
			return nil, nil, err
		}

		def, err := ctr.Marshal(ctx, pg)
		if err != nil {
			return nil, nil, err
		}

		res, err := client.Solve(ctx, gwclient.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, nil, err
		}

		img, err := linux.BuildImageConfig(ctx, sOpt, spec, platform, targetKey)
		if err != nil {
			return nil, nil, err
		}

		ref, err := res.SingleRef()
		if err != nil {
			return nil, nil, err
		}

		return ref, img, nil
	})
}
