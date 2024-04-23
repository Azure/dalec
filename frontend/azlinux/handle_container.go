package azlinux

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

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

			pg := dalec.ProgressGroup("Build mariner2 container: " + spec.Name)

			rpmDir, err := specToRpmLLB(w, client, spec, sOpt, targetKey, pg)
			if err != nil {
				return nil, nil, fmt.Errorf("error creating rpm: %w", err)
			}

			rpms, err := readRPMs(ctx, client, rpmDir)
			if err != nil {
				return nil, nil, err
			}

			st, err := specToContainerLLB(w, client, spec, targetKey, rpmDir, rpms, sOpt, pg)
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

			var img *dalec.DockerImageSpec
			if base := frontend.GetBaseOutputImage(spec, targetKey, ""); base != "" {
				_, _, dt, err := client.ResolveImageConfig(ctx, base, sourceresolver.Opt{})
				if err != nil {
					return nil, nil, errors.Wrap(err, "error resolving base image config")
				}
				var cfg dalec.DockerImageSpec
				if err := json.Unmarshal(dt, &cfg); err != nil {
					return nil, nil, errors.Wrap(err, "error unmarshalling base image config")
				}
				img = &cfg
			} else {
				img, err = w.DefaultImageConfig(ctx, client)
				if err != nil {
					return nil, nil, errors.Wrap(err, "could not get default image config")
				}
			}

			// TODO: DNM: This is not merging the image config from the spec.
			specImg := frontend.MergeSpecImage(spec, targetKey)

			err = dalec.MergeImageConfig(&img.Config, specImg)
			if err != nil {
				return nil, nil, err
			}

			ref, err := res.SingleRef()
			if err != nil {
				return nil, nil, err
			}

			if err := frontend.RunTests(ctx, client, spec, ref, targetKey); err != nil {
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

func specToContainerLLB(w worker, client gwclient.Client, spec *dalec.Spec, target string, rpmDir llb.State, files []string, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, error) {
	opts = append(opts, dalec.ProgressGroup("Install RPMs"))
	const workPath = "/tmp/rootfs"

	builderImg := w.Base(client, opts...)

	// TODO: This is mariner specific and should probably be moved out of this
	// package.
	mfstDir := filepath.Join(workPath, "var/lib/rpmmanifest")
	mfst1 := filepath.Join(mfstDir, "container-manifest-1")
	mfst2 := filepath.Join(mfstDir, "container-manifest-2")
	rpmdbDir := filepath.Join(workPath, "var/lib/rpm")

	chrootedPaths := []string{
		filepath.Join(workPath, "/usr/local/bin"),
		filepath.Join(workPath, "/usr/local/sbin"),
		filepath.Join(workPath, "/usr/bin"),
		filepath.Join(workPath, "/usr/sbin"),
		filepath.Join(workPath, "/bin"),
		filepath.Join(workPath, "/sbin"),
	}
	chrootedPathEnv := strings.Join(chrootedPaths, ":")

	rootfs := llb.Scratch()
	if ref := frontend.GetBaseOutputImage(spec, target, ""); ref != "" {
		rootfs = llb.Image(ref, llb.WithMetaResolver(sOpt.Resolver), dalec.WithConstraints(opts...))
	}

	if len(files) > 0 {
		rpmMountDir := "/tmp/rpms"
		updated := make([]string, 0, len(files))
		for _, f := range files {
			updated = append(updated, filepath.Join(rpmMountDir, f))
		}

		rootfs = builderImg.Run(
			w.Install(workPath, updated, true),
			llb.AddMount(rpmMountDir, rpmDir, llb.SourcePath("/RPMS")),
			dalec.WithConstraints(opts...),
		).AddMount(workPath, rootfs)
	}

	manifestCmd := `
#!/usr/bin/env sh

# If the rpm command is in the rootfs then we don't need to do anything
# If not then this is a distroless image and we need to generate manifests of the installed rpms and cleanup the rpmdb.

PATH="` + chrootedPathEnv + `" command -v rpm && exit 0

set -e
mkdir -p ` + mfstDir + `
rpm --dbpath=` + rpmdbDir + ` -qa > ` + mfst1 + `
rpm --dbpath=` + rpmdbDir + ` -qa --qf "%{NAME}\t%{VERSION}-%{RELEASE}\t%{INSTALLTIME}\t%{BUILDTIME}\t%{VENDOR}\t(none)\t%{SIZE}\t%{ARCH}\t%{EPOCHNUM}\t%{SOURCERPM}\n" > ` + mfst2 + `
rm -rf ` + rpmdbDir + `
`

	manifestSh := llb.Scratch().File(llb.Mkfile("manifest.sh", 0o755, []byte(manifestCmd)), opts...)
	rootfs = builderImg.
		Run(
			shArgs("/tmp/manifest.sh"),
			llb.AddMount("/tmp/manifest.sh", manifestSh, llb.SourcePath("manifest.sh")),
			dalec.WithConstraints(opts...),
		).AddMount(workPath, rootfs)

	if post := getImagePostInstall(spec, target); post != nil && len(post.Symlinks) > 0 {
		rootfs = builderImg.
			Run(dalec.WithConstraints(opts...), addImagePost(post, workPath)).
			AddMount(workPath, rootfs)
	}

	return rootfs, nil
}

func getImagePostInstall(spec *dalec.Spec, targetKey string) *dalec.PostInstall {
	tgt, ok := spec.Targets[targetKey]
	if ok && tgt.Image != nil && tgt.Image.Post != nil {
		return tgt.Image.Post
	}

	if spec.Image == nil {
		return nil
	}
	return spec.Image.Post
}

func addImagePost(post *dalec.PostInstall, rootfsPath string) llb.RunOption {
	return runOptionFunc(func(ei *llb.ExecInfo) {
		if post == nil {
			return
		}

		if len(post.Symlinks) == 0 {
			return
		}

		buf := bytes.NewBuffer(nil)
		buf.WriteString("set -ex\n")
		fmt.Fprintf(buf, "cd %q\n", rootfsPath)

		for src, tgt := range post.Symlinks {
			fmt.Fprintf(buf, "ln -s %q %q\n", src, filepath.Join(rootfsPath, tgt.Path))
		}
		shArgs(buf.String()).SetRunOption(ei)
		dalec.ProgressGroup("Add post-install symlinks").SetRunOption(ei)
	})
}

type runOptionFunc func(*llb.ExecInfo)

func (f runOptionFunc) SetRunOption(ei *llb.ExecInfo) {
	f(ei)
}
