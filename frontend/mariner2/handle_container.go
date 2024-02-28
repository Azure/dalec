package mariner2

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/google/shlex"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/image"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/pkg/errors"
)

const (
	marinerDistrolessRef = "mcr.microsoft.com/cbl-mariner/distroless/base:2.0"
)

func handleContainer(ctx context.Context, client gwclient.Client, spec *dalec.Spec) (gwclient.Reference, *image.Image, error) {
	sOpt, err := frontend.SourceOptFromClient(ctx, client)
	if err != nil {
		return nil, nil, err
	}

	pg := dalec.ProgressGroup("Build mariner2 container: " + spec.Name)
	baseImg := getWorkerImage(sOpt, pg)

	rpmDirs, err := buildRPMDirs(spec, baseImg, sOpt, pg)
	if err != nil {
		return nil, nil, err
	}

	st, err := specsToContainerLLB(spec, targetKey, baseImg, rpmDirs, sOpt, pg)
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

	img, err := buildImageConfig(ctx, spec, targetKey, client)
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
}

func buildRPMDirs(spec *dalec.Spec, baseImg llb.State, sOpt dalec.SourceOpts, pg llb.ConstraintsOpt) (map[string]llb.State, error) {
	mutRPMDirs := make(map[string]llb.State)

	switch dalec.BuildGraph.Len(spec.Name) {
	case 0:
		return nil, fmt.Errorf("no specs found in build graph")
	// case 1:
	// 	rpmDirs := mutRPMDirs
	// 	return rpmDirs, nil
	default:
	}

	orderedDeps := dalec.BuildGraph.OrderedSlice(spec.Name)

	for _, depSpec := range orderedDeps {
		if err := updateRPMDirs(depSpec, mutRPMDirs, baseImg, spec, sOpt, pg); err != nil {
			return nil, err
		}
	}

	rpmDirs := mutRPMDirs
	return rpmDirs, nil
}

func updateRPMDirs(depSpec *dalec.Spec, rpmDirs map[string]llb.State, baseImg llb.State, spec *dalec.Spec, sOpt dalec.SourceOpts, pg llb.ConstraintsOpt) error {
	bDeps, _ := partitionBuildDeps(depSpec)

	rpms := llb.Scratch()
	for _, bd := range bDeps {
		rpmfileState, ok := rpmDirs[bd]

		// by definition, the first spec in the graph will have no
		// dependencies, so the below should never happen
		// execute build
		if !ok {
			return fmt.Errorf("dependency ordering error: rpm state %q not found", bd)
		}

		const outdir = "/tmp/buildrpms"
		rpms = baseImg.Run(
			shArgs(`find /tmp/mountedrpm -type f -name "*.rpm" -not -name "*.src.rpm" -exec cp {} `+outdir+` \;`),
			llb.AddMount("/tmp/mountedrpm", rpmfileState),
		).AddMount(outdir, rpms)
	}

	rpmState, err := specToRpmLLBWithBuildDeps(depSpec, sOpt, rpms, pg)
	if err != nil {
		return fmt.Errorf("error building dependency %q", depSpec.Name)
	}

	rpmDirs[depSpec.Name] = rpmState
	return nil
}

func buildImageConfig(ctx context.Context, spec *dalec.Spec, target string, client gwclient.Client) (*image.Image, error) {
	dc, err := dockerui.NewClient(client)
	if err != nil {
		return nil, err
	}

	baseImgRef := getBaseOutputImage(spec, targetKey)
	_, _, dt, err := client.ResolveImageConfig(ctx, baseImgRef, llb.ResolveImageConfigOpt{
		ResolveMode: dc.ImageResolveMode.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("error resolving image config: %w", err)
	}

	var img image.Image
	if err := json.Unmarshal(dt, &img); err != nil {
		return nil, fmt.Errorf("error unmarshalling image config: %w", err)
	}

	if err := copyImageConfig(&img, mergeSpecImage(spec, targetKey)); err != nil {
		return nil, err
	}

	return &img, nil
}

func mergeSpecImage(spec *dalec.Spec, target string) *dalec.ImageConfig {
	var cfg dalec.ImageConfig

	if spec.Image != nil {
		cfg = *spec.Image
	}

	if i := spec.Targets[target].Image; i != nil {
		if i.Entrypoint != "" {
			cfg.Entrypoint = spec.Targets[target].Image.Entrypoint
		}

		if i.Cmd != "" {
			cfg.Cmd = spec.Targets[target].Image.Cmd
		}

		cfg.Env = append(cfg.Env, i.Env...)

		for k, v := range i.Volumes {
			cfg.Volumes[k] = v
		}

		for k, v := range i.Labels {
			cfg.Labels[k] = v
		}

		if i.WorkingDir != "" {
			cfg.WorkingDir = i.WorkingDir
		}

		if i.StopSignal != "" {
			cfg.StopSignal = i.StopSignal
		}

		if i.Base != "" {
			cfg.Base = i.Base
		}
	}

	return &cfg
}

func getBaseOutputImage(spec *dalec.Spec, target string) string {
	baseRef := marinerDistrolessRef
	if spec.Targets[target].Image != nil && spec.Targets[target].Image.Base != "" {
		baseRef = spec.Targets[target].Image.Base
	}
	return baseRef
}

func specsToContainerLLB(spec *dalec.Spec, target string, builderImg llb.State, rpmDirs map[string]llb.State, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, error) {
	opts = append(opts, dalec.ProgressGroup("Install RPMs"))
	const workPath = "/tmp/rootfs"

	// return llb.Scratch(), fmt.Errorf("ERROR: %#v", maps.Keys(rpmDirs))

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

	installCmd := `
#!/usr/bin/env sh
set -ex

mkdir -p /tmp/rpms
find /tmp/rpms -type f -name "*.rpm" -not -name "*.src.rpm" -print0 |
	xargs -0 tdnf -v install --releasever=2.0 -y --nogpgcheck --installroot "` + workPath + `" --setopt=reposdir=/etc/yum.repos.d || exit

# If the rpm command is in the rootfs then we don't need to do anything
# If not then this is a distroless image and we need to generate manifests of the installed rpms and cleanup the rpmdb.

PATH="` + chrootedPathEnv + `" command -v rpm && exit 0

set -e
mkdir -p ` + mfstDir + `
rpm --dbpath=` + rpmdbDir + ` -qa > ` + mfst1 + `
rpm --dbpath=` + rpmdbDir + ` -qa --qf "%{NAME}\t%{VERSION}-%{RELEASE}\t%{INSTALLTIME}\t%{BUILDTIME}\t%{VENDOR}\t(none)\t%{SIZE}\t%{ARCH}\t%{EPOCHNUM}\t%{SOURCERPM}\n" > ` + mfst2 + `
rm -rf ` + rpmdbDir + `
`

	installer := llb.Scratch().File(llb.Mkfile("install.sh", 0o755, []byte(installCmd)), opts...)

	baseImg := llb.Image(getBaseOutputImage(spec, target), llb.WithMetaResolver(sOpt.Resolver), dalec.WithConstraints(opts...))
	runArgs := []llb.RunOption{
		shArgs("/tmp/install.sh"),
		defaultTndfCacheMount(),
		llb.AddMount("/tmp/install.sh", installer, llb.SourcePath("install.sh")),
		// Mount the tdnf cache into the workpath so that:
		// 1. tdnf will use the cache
		// 2. Repo data and packages are not left behind in the final image.
		tdnfCacheMountWithPrefix(workPath),
		dalec.WithConstraints(opts...),
	}

	runDeps := getRuntimeDepSet(spec)
	for name, rpmDir := range rpmDirs {
		if !runDeps.Has(name) && name != spec.Name {
			continue
		}
		runArgs = append(
			runArgs,
			llb.AddMount(filepath.Join("/tmp/rpms", name), rpmDir, llb.SourcePath("/RPMS")),
		)
	}
	worker := builderImg.Run(runArgs...)

	// This adds a mount to the worker so that all the commands are run with this mount added
	// The return value is the state representing the contents of the mounted directory after the commands are run
	rootfs := worker.AddMount(workPath, baseImg)

	return rootfs, nil
}

func specToContainerLLB(spec *dalec.Spec, target string, builderImg llb.State, rpmDir llb.State, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, error) {
	opts = append(opts, dalec.ProgressGroup("Install RPMs"))
	const workPath = "/tmp/rootfs"

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

	installCmd := `
#!/usr/bin/env sh

check_non_empty() {
	ls ${1} > /dev/null 2>&1
}

arch_dir="/tmp/rpms/$(uname -m)"
noarch_dir="/tmp/rpms/noarch"

rpms=""

if check_non_empty "${noarch_dir}/*.rpm"; then
	rpms="${noarch_dir}/*.rpm"
fi

if check_non_empty "${arch_dir}/*.rpm"; then
	rpms="${rpms} ${arch_dir}/*.rpm"
fi

if [ -n "${rpms}" ]; then
	tdnf -v install --releasever=2.0 -y --nogpgcheck --installroot "` + workPath + `" --setopt=reposdir=/etc/yum.repos.d ${rpms} || exit
fi

# If the rpm command is in the rootfs then we don't need to do anything
# If not then this is a distroless image and we need to generate manifests of the installed rpms and cleanup the rpmdb.

PATH="` + chrootedPathEnv + `" command -v rpm && exit 0

set -e
mkdir -p ` + mfstDir + `
rpm --dbpath=` + rpmdbDir + ` -qa > ` + mfst1 + `
rpm --dbpath=` + rpmdbDir + ` -qa --qf "%{NAME}\t%{VERSION}-%{RELEASE}\t%{INSTALLTIME}\t%{BUILDTIME}\t%{VENDOR}\t(none)\t%{SIZE}\t%{ARCH}\t%{EPOCHNUM}\t%{SOURCERPM}\n" > ` + mfst2 + `
rm -rf ` + rpmdbDir + `
`

	installer := llb.Scratch().File(llb.Mkfile("install.sh", 0o755, []byte(installCmd)), opts...)

	baseImg := llb.Image(getBaseOutputImage(spec, target), llb.WithMetaResolver(sOpt.Resolver), dalec.WithConstraints(opts...))
	worker := builderImg.
		Run(
			shArgs("/tmp/install.sh"),
			defaultTndfCacheMount(),
			llb.AddMount("/tmp/rpms", rpmDir, llb.SourcePath("/RPMS")),
			llb.AddMount("/tmp/install.sh", installer, llb.SourcePath("install.sh")),
			// Mount the tdnf cache into the workpath so that:
			// 1. tdnf will use the cache
			// 2. Repo data and packages are not left behind in the final image.
			tdnfCacheMountWithPrefix(workPath),
			dalec.WithConstraints(opts...),
		)

	// This adds a mount to the worker so that all the commands are run with this mount added
	// The return value is the state representing the contents of the mounted directory after the commands are run
	rootfs := worker.AddMount(workPath, baseImg)

	if post := getImagePostInstall(spec, target); post != nil && len(post.Symlinks) > 0 {
		rootfs = worker.
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

func copyImageConfig(dst *image.Image, src *dalec.ImageConfig) error {
	if src == nil {
		return nil
	}

	if src.Entrypoint != "" {
		split, err := shlex.Split(src.Entrypoint)
		if err != nil {
			return errors.Wrap(err, "error splitting entrypoint into args")
		}
		dst.Config.Entrypoint = split
		// Reset cmd as this may be totally invalid now
		// This is the same behavior as the Dockerfile frontend
		dst.Config.Cmd = nil
	}
	if src.Cmd != "" {
		split, err := shlex.Split(src.Cmd)
		if err != nil {
			return errors.Wrap(err, "error splitting cmd into args")
		}
		dst.Config.Cmd = split
	}

	if len(src.Env) > 0 {
		// Env is append only
		// If the env var already exists, replace it
		envIdx := make(map[string]int)
		for i, env := range dst.Config.Env {
			envIdx[env] = i
		}

		for _, env := range src.Env {
			if idx, ok := envIdx[env]; ok {
				dst.Config.Env[idx] = env
			} else {
				dst.Config.Env = append(dst.Config.Env, env)
			}
		}
	}

	if src.WorkingDir != "" {
		dst.Config.WorkingDir = src.WorkingDir
	}
	if src.StopSignal != "" {
		dst.Config.StopSignal = src.StopSignal
	}

	return nil
}
