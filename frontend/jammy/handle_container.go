package jammy

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

const jammyRef = "mcr.microsoft.com/mirror/docker/library/ubuntu:jammy"

// Unlike how azlinux works, when creating a debian system there are many implicit
// dpeendencies which are already expected to be on the system that are not in
// the package dependency tree.
// These are the extra packages we'll include when building a container.
var (
	baseDeps = []string{
		"base-files",
		"base-passwd",
		"usrmerge",
	}
	systemdDeps = []string{
		"init-system-helpers",
		"bash",
		"systemctl",
		"dash",
	}
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

		st := buildImageRootfs(spec, sOpt, deb, targetKey, opt)

		def, err := st.Marshal(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("error marshalling llb: %w", err)
		}

		img, err := buildImageConfig(ctx, client, spec, platform, targetKey)
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

		if err := frontend.RunTests(ctx, client, spec, ref, installTestDeps(sOpt.Resolver, spec, targetKey, opt), targetKey); err != nil {
			return nil, nil, err
		}

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

func aptWorker(opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		return in.With(installPackages(dalec.WithConstraints(opts...), "apt-utils", "mmdebstrap"))
	}
}

func buildImageRootfs(spec *dalec.Spec, sOpt dalec.SourceOpts, deb llb.State, targetKey string, opts ...llb.ConstraintsOpt) llb.State {
	base := dalec.GetBaseOutputImage(spec, targetKey)

	worker := workerBase(sOpt.Resolver).With(aptWorker(opts...))
	repoMount := addAptRepoForDeb(worker, deb)

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

	// When no base iamge is proided we create a new rootfs from scratch
	// This requries some special handling.
	// When there is a base image we assume apt-get to be available.
	if base == "" {
		base = jammyRef
	}

	baseImg := llb.Image(base, llb.WithMetaResolver(sOpt.Resolver))
	return buildRootfsFromBase(baseImg, repoMount, spec, opts...).With(installSymlinks)
}

// buildRootfsFromBase creates a rootfs from a base image and installs the built package and all its runtime dependencies
// This requires apt-get and /bin/sh to be installed in the base image
func buildRootfsFromBase(base llb.State, repoMount llb.RunOption, spec *dalec.Spec, opts ...llb.ConstraintsOpt) llb.State {
	debug := llb.Scratch().File(llb.Mkfile("debug", 0o644, []byte(`debug=2`)), opts...)
	opts = append(opts, dalec.ProgressGroup("Install spec package"))
	return base.Run(
		dalec.ShArgs("apt-get update && apt-get install -y "+spec.Name),
		llb.AddEnv("DEBIAN_FRONTEND", "noninteractive"),
		dalec.WithMountedAptCache(aptCachePrefix),
		repoMount,
		dalec.WithConstraints(opts...),
		llb.AddMount("/etc/dpkg/dpkg.cfg.d/99-dalec-debug", debug, llb.SourcePath("debug"), llb.Readonly),
	).Root()
}

// debstrap produces a command suitable for using mmdebstrap to install depdendencies.
// It is assumed that the location that debs should be installed to is /tmp/rootfs
// It is also assumed that the apt sources is at /etc/apt/sources.list
// This also removes all apt artifacts.
func debstrap(packages []string) llb.RunOption {
	return dalec.ShArgs("set -ex; apt-get update; mmdebstrap --debug --verbose --variant=essential --mode=chrootless --include=" + strings.Join(packages, ",") + " jammy /tmp/rootfs /etc/apt/sources.list; rm -rf /tmp/rootfs/var/lib/apt; rm -rf /tmp/rootfs/var/cache/apt")
}

func shouldIncludeSystemdDeps(spec *dalec.Spec) bool {
	if spec.Artifacts.Systemd.IsEmpty() {
		return false
	}
	return len(spec.Artifacts.Systemd.Units) > 0
}

func getPackageList(spec *dalec.Spec) []string {
	// pkgs := baseDeps
	var pkgs []string
	//if shouldIncludeSystemdDeps(spec) {
	//	pkgs = append(pkgs, systemdDeps...)
	//}

	pkgs = append(pkgs, spec.Name)
	return pkgs
}

// bootstrapRootfs creates a rootfs from scratch using the built package and all its runtime dependencies
func boostrapRootfs(worker llb.State, repoMount llb.RunOption, spec *dalec.Spec, opts ...llb.ConstraintsOpt) llb.State {
	opts = append(opts, dalec.ProgressGroup("Bootstrap rootfs"))
	return worker.
		Run(
			// llb.Security(llb.SecurityModeInsecure),
			debstrap(getPackageList(spec)),
			llb.AddEnv("DEBIAN_FRONTEND", "noninteractive"),
			repoMount,
			dalec.WithConstraints(opts...),
			dalec.WithMountedAptCache(aptCachePrefix),
		).
		AddMount("/tmp/rootfs", llb.Scratch())
}

// addAptRepoForDeb creates a local package repository containing the packages  in the provided llb state.
// This is used so that `apt` (or mmdebstrap) can be used to install our packages with dependency resolution.
func addAptRepoForDeb(base llb.State, deb llb.State, opts ...llb.ConstraintsOpt) llb.RunOption {
	return dalec.RunOptFunc(func(ei *llb.ExecInfo) {
		opts = append(opts, dalec.ProgressGroup("Setup local apt repo"))
		repo := base.
			Run(
				dalec.ShArgs("apt-ftparchive packages . | gzip -1 > Packages.gz"),
				llb.Dir("/tmp/deb"),
				dalec.WithConstraints(opts...),
			).
			AddMount("/tmp/deb", deb)

		sources := base.
			Run(
				dalec.ShArgs("set -e; echo 'deb [trusted=yes] copy:/tmp/_dalec_apt_repo_deb/ /' > /tmp/sources/list; cat /etc/apt/sources.list >> /tmp/sources/list"),
				dalec.WithConstraints(opts...),
			).
			AddMount("/tmp/sources", llb.Scratch())

		llb.AddMount("/tmp/_dalec_apt_repo_deb", repo, llb.Readonly).SetRunOption(ei)
		llb.AddMount("/etc/apt/sources.list", sources, llb.Readonly, llb.SourcePath("list")).SetRunOption(ei)
	})
}

func installTestDeps(resolver llb.ImageMetaResolver, spec *dalec.Spec, targetKey string, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		deps := spec.GetTestDeps(targetKey)
		if len(deps) == 0 {
			return in
		}

		opts = append(opts, dalec.ProgressGroup("Install test dependencies"))

		noBaseImg := dalec.GetBaseOutputImage(spec, targetKey) == ""

		// When building the actual container we differentiate whether or not to use
		// mmdebstrap or straight apt-get based on wehter or not the spec includes a
		// custom base image to use.
		// It ia assumed that if a custom base is provided then it is able to use apt-get.
		// Since we are installing more packages to the container image we'll use that same logic here.
		if noBaseImg {
			worker := workerBase(resolver).With(aptWorker(opts...))
			return worker.Run(
				debstrap(deps),
				llb.Security(llb.SecurityModeInsecure),
				llb.AddEnv("DEBIAN_FRONTEND", "noninteractive"),
			).
				AddMount("/tmp/rootfs", in)
		}

		return in.Run(
			dalec.ShArgs("apt-get update && apt-get install -y --no-install-recommends "+strings.Join(deps, " ")),
			llb.AddEnv("DEBIAN_FRONTEND", "noninteractive"),
			dalec.WithMountedAptCache(aptCachePrefix),
		).Root()
	}
}
