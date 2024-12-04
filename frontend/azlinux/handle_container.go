package azlinux

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

func handleContainer(w worker) gwclient.BuildFunc {
	return func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
		return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
			sOpt, err := frontend.SourceOptFromClient(ctx, client)
			if err != nil {
				return nil, nil, err
			}

			pg := dalec.ProgressGroup("Building " + targetKey + " container: " + spec.Name)

			rpmDir, err := specToRpmLLB(ctx, w, client, spec, sOpt, targetKey, pg)
			if err != nil {
				return nil, nil, fmt.Errorf("error creating rpm: %w", err)
			}

			img, err := resolveBaseConfig(ctx, w, client, platform, spec, targetKey)
			if err != nil {
				return nil, nil, errors.Wrap(err, "could not resolve base image config")
			}

			ref, err := runTests(ctx, client, w, spec, sOpt, rpmDir, targetKey)
			return ref, img, err
		})
	}
}

func specToContainerLLB(w worker, spec *dalec.Spec, targetKey string, rpmDir llb.State, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, error) {
	opts = append(opts, dalec.ProgressGroup("Install RPMs"))
	const workPath = "/tmp/rootfs"

	builderImg, err := w.Base(sOpt, opts...)
	if err != nil {
		return llb.Scratch(), err
	}

	rootfs := llb.Scratch()
	if ref := dalec.GetBaseOutputImage(spec, targetKey); ref != "" {
		rootfs = llb.Image(ref, llb.WithMetaResolver(sOpt.Resolver), dalec.WithConstraints(opts...))
	}

	installTimeRepos := spec.GetInstallRepos(targetKey)
	importRepos, err := repoMountInstallOpts(installTimeRepos, sOpt, opts...)
	if err != nil {
		return llb.Scratch(), err
	}

	rpmMountDir := "/tmp/rpms"
	pkgs := w.BasePackages()
	pkgs = append(pkgs, filepath.Join(rpmMountDir, "**/*.rpm"))

	installOpts := []installOpt{atRoot(workPath)}
	installOpts = append(installOpts, importRepos...)
	installOpts = append(installOpts, []installOpt{noGPGCheck, installWithConstraints(opts)}...)

	rootfs = builderImg.Run(
		w.Install(pkgs, installOpts...),
		llb.AddMount(rpmMountDir, rpmDir, llb.SourcePath("/RPMS")),
		dalec.WithConstraints(opts...),
	).AddMount(workPath, rootfs)

	if post := spec.GetImagePost(targetKey); post != nil && len(post.Symlinks) > 0 {
		rootfs = rootfs.With(dalec.InstallPostSymlinks(builderImg, post, workPath, opts...))
	}

	return rootfs, nil
}

func resolveBaseConfig(ctx context.Context, w worker, resolver llb.ImageMetaResolver, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (*dalec.DockerImageSpec, error) {
	var img *dalec.DockerImageSpec

	if ref := dalec.GetBaseOutputImage(spec, targetKey); ref != "" {
		_, _, dt, err := resolver.ResolveImageConfig(ctx, ref, sourceresolver.Opt{Platform: platform})
		if err != nil {
			return nil, errors.Wrap(err, "error resolving base image config")
		}

		var i dalec.DockerImageSpec
		if err := json.Unmarshal(dt, &i); err != nil {
			return nil, errors.Wrap(err, "error unmarshalling base image config")
		}
		img = &i
	} else {
		var err error
		img, err = w.DefaultImageConfig(ctx, resolver, platform)
		if err != nil {
			return nil, errors.Wrap(err, "error resolving default image config")
		}
	}

	if err := dalec.BuildImageConfig(spec, targetKey, img); err != nil {
		return nil, err
	}
	return img, nil
}
