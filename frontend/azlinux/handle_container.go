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

			rpms, err := readRPMs(ctx, client, rpmDir)
			if err != nil {
				return nil, nil, err
			}

			st, err := specToContainerLLB(w, spec, targetKey, rpmDir, rpms, sOpt, pg)
			if err != nil {
				return nil, nil, err
			}

			def, err := st.Marshal(ctx, pg)
			if err != nil {
				return nil, nil, fmt.Errorf("error marshalling llb: %w", err)
			}

			res, err := client.Solve(ctx, gwclient.SolveRequest{
				Definition: def.ToPB(),
			})
			if err != nil {
				return nil, nil, err
			}

			img, err := resolveBaseConfig(ctx, w, client, platform, spec, targetKey)
			if err != nil {
				return nil, nil, errors.Wrap(err, "could not resolve base image config")
			}

			ref, err := res.SingleRef()
			if err != nil {
				return nil, nil, err
			}

			base, err := w.Base(sOpt, pg)
			if err != nil {
				return nil, nil, err
			}

			withTestDeps := func(in llb.State) llb.State {
				deps := spec.GetTestDeps(targetKey)
				if len(deps) == 0 {
					return in
				}
				return base.Run(
					w.Install(spec.GetTestDeps(targetKey), dalec.NoOption(), atRoot("/tmp/rootfs")),
					pg,
					dalec.ProgressGroup("Install test dependencies"),
				).AddMount("/tmp/rootfs", in)
			}

			if err := frontend.RunTests(ctx, client, spec, ref, withTestDeps, targetKey); err != nil {
				return nil, nil, err
			}

			return ref, img, err
		})
	}
}

func readRPMs(ctx context.Context, client gwclient.Client, st llb.State) ([]string, error) {
	def, err := st.Marshal(ctx)
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

	// Directory layout will have arch-specific sub-folders and/or `noarch`
	// RPMs will be in those subdirectories.
	arches, err := ref.ReadDir(ctx, gwclient.ReadDirRequest{
		Path: "/RPMS",
	})
	if err != nil {
		return nil, errors.Wrap(err, "error reading output state")
	}

	var out []string

	for _, arch := range arches {
		files, err := ref.ReadDir(ctx, gwclient.ReadDirRequest{
			Path:           filepath.Join("/RPMS", arch.Path),
			IncludePattern: "*.rpm",
		})

		if err != nil {
			return nil, errors.Wrap(err, "could not read arch specific output dir")
		}

		for _, e := range files {
			out = append(out, filepath.Join(arch.Path, e.Path))
		}
	}

	return out, nil
}

func specToContainerLLB(w worker, spec *dalec.Spec, targetKey string, rpmDir llb.State, files []string, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, error) {
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

	if len(files) > 0 {
		rpmMountDir := "/tmp/rpms"
		updated := w.BasePackages()
		for _, f := range files {
			updated = append(updated, filepath.Join(rpmMountDir, f))
		}

		rootfs = builderImg.Run(
			w.Install(updated, dalec.NoOption(), atRoot(workPath), noGPGCheck, withManifests, installWithConstraints(opts)),
			llb.AddMount(rpmMountDir, rpmDir, llb.SourcePath("/RPMS")),
			dalec.WithConstraints(opts...),
		).AddMount(workPath, rootfs)
	}

	if post := spec.GetImagePost(targetKey); post != nil && len(post.Symlinks) > 0 {
		rootfs = builderImg.
			Run(dalec.WithConstraints(opts...), dalec.InstallPostSymlinks(post, workPath)).
			AddMount(workPath, rootfs)
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
