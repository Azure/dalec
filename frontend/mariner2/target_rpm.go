package mariner2

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/azure/dalec"
	"github.com/azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/image"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
)

const (
	marinerRef      = "mcr.microsoft.com/cbl-mariner/base/core:2.0"
	toolchainImgRef = "ghcr.io/azure/dalec/mariner2/toolchain:latest"

	cachedToolkitRPMDir = "/root/.cache/mariner2-toolkit-rpm-cache"
	marinerToolkitPath  = "/usr/local/toolkit"
)

var (
	marinerTdnfCache = llb.AddMount("/var/tdnf/cache", llb.Scratch(), llb.AsPersistentCacheDir("mariner2-tdnf-cache", llb.CacheMountLocked))
)

func handleRPM(ctx context.Context, client gwclient.Client, spec *dalec.Spec) (gwclient.Reference, *image.Image, error) {
	caps := client.BuildOpts().LLBCaps
	noMerge := !caps.Contains(pb.CapMergeOp)

	st, err := specToRpmLLB(spec, noMerge, getDigestFromClientFn(ctx, client), client, frontend.ForwarderFromClient(ctx, client))
	if err != nil {
		return nil, nil, err
	}

	def, err := st.Marshal(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("error marshalling llb: %w", err)
	}

	res, err := client.Solve(ctx, gwclient.SolveRequest{
		Definition: def.ToPB(),
	})
	if err != nil {
		return nil, nil, err
	}
	ref, err := res.SingleRef()
	// Do not return a nil image, it may cause a panic
	return ref, &image.Image{}, err
}

func shArgs(cmd string) llb.RunOption {
	return llb.Args([]string{"sh", "-c", cmd})
}

func getBuildDeps(spec *dalec.Spec) []string {
	var deps *dalec.PackageDependencies
	if t, ok := spec.Targets[targetKey]; ok {
		deps = t.Dependencies
	}

	if deps == nil {
		deps = spec.Dependencies
		if deps == nil {
			return nil
		}
	}

	var out []string
	for p := range deps.Build {
		out = append(out, p)
	}

	sort.Strings(out)
	return out
}

func specToRpmLLB(spec *dalec.Spec, noMerge bool, getDigest getDigestFunc, mr llb.ImageMetaResolver, forward dalec.ForwarderFunc) (llb.State, error) {
	br, err := spec2ToolkitRootLLB(spec, noMerge, getDigest, mr, forward)
	if err != nil {
		return llb.Scratch(), err
	}

	st := llb.Image(toolchainImgRef, llb.WithMetaResolver(mr)).
		// /.dockerenv is used by the toolkit to detect it's running in a container.
		// This makes the toolkit use a different strategy for setting up chroots.
		// Namely, it makes so the toolkit won't use "mount" to mount the stuff into the chroot which requires CAP_SYS_ADMIN.
		// (CAP_SYS_ADMIN is not enabled in our build).
		File(llb.Mkfile("/.dockerenv", 0o600, []byte{})).
		Dir("/build/toolkit").
		AddEnv("SPECS_DIR", "/build/SPECS-dalec").
		AddEnv("CONFIG_FILE", ""). // This is needed for VM images(?), don't need this for our case anyway and the default value is wrong for us.
		AddEnv("OUT_DIR", "/build/out").
		AddEnv("LOG_LEVEL", "debug").
		AddEnv("CACHED_RPMS_DIR", cachedToolkitRPMDir).
		Run(
			shArgs("dnf download -y --releasever=2.0 --resolve --alldeps --downloaddir \"${CACHED_RPMS_DIR}/cache\" "+strings.Join(getBuildDeps(spec), " ")),
			llb.AddMount(cachedToolkitRPMDir, llb.Scratch(), llb.AsPersistentCacheDir("mariner2-toolkit-rpm-cache", llb.CacheMountLocked)),
		).
		Run(
			shArgs("make -j$(nproc) build-packages || (cat /build/logs/pkggen/rpmbuilding/*; exit 1)"),
			llb.AddMount(cachedToolkitRPMDir, llb.Scratch(), llb.AsPersistentCacheDir("mariner2-toolkit-rpm-cache", llb.CacheMountLocked)),
			// Mount cached packages into the chroot dirs so they are available to the chrooted build.
			// The toolchain has built-in (yum) repo files that points to "/upstream-cached-rpms",
			// so "tdnf install" as performed by the toolchain will read files from this location in the chroot.
			//
			// The toolchain can also run things in parallel so it will use multiple chroots.
			// See https://github.com/microsoft/CBL-Mariner/blob/8b1db59e9b011798e8e7750907f58b1bc9577da7/toolkit/tools/internal/buildpipeline/buildpipeline.go#L37-L117 for implementation of this.
			//
			// This is needed because the toolkit cannot mount anything into the chroot as it doesn't have CAP_SYS_ADMIN in our build.
			// So we have to mount the cached packages into the chroot dirs ourselves.
			runOptFunc(func(ei *llb.ExecInfo) {
				for i := 0; i < 8; i++ {
					llb.AddMount("/tmp/chroot/dalec"+strconv.Itoa(i)+"/upstream-cached-rpms", llb.Scratch(), llb.AsPersistentCacheDir("mariner2-toolkit-rpm-cache", llb.CacheMountLocked), llb.SourcePath("/cache")).SetRunOption(ei)
				}
			}),

			llb.AddMount("/build/SPECS-dalec", br, llb.SourcePath("/SPECS")),
			llb.AddEnv("VERSION", spec.Version),
			llb.AddEnv("BUILD_NUMBER", spec.Revision),
			llb.AddEnv("REFRESH_WORKER_CHROOT", "n"),
		).State

	return llb.Scratch().File(
		llb.Copy(st, "/build/out", "/", dalec.WithDirContentsOnly(), dalec.WithIncludes([]string{"RPMS", "SRPMS"})),
	), nil
}

type runOptFunc func(*llb.ExecInfo)

func (f runOptFunc) SetRunOption(ei *llb.ExecInfo) {
	f(ei)
}
