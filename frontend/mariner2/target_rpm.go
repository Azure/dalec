package mariner2

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/image"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/identity"
)

const (
	marinerRef            = "mcr.microsoft.com/cbl-mariner/base/core:2.0"
	toolchainImgRef       = "ghcr.io/azure/dalec/mariner2/toolchain:latest"
	toolchainNamedContext = "mariner2-toolchain"

	tookitRpmsCacheDir = "/root/.cache/mariner2-toolkit-rpm-cache"
	cachedRpmsName     = "mariner2-toolkit-cached-rpms"
	marinerToolkitPath = "/usr/local/toolkit"
)

var (
	marinerTdnfCache = llb.AddMount("/var/cache/tdnf", llb.Scratch(), llb.AsPersistentCacheDir("mariner2-tdnf-cache", llb.CacheMountLocked))
)

func handleRPM(ctx context.Context, client gwclient.Client, spec *dalec.Spec) (gwclient.Reference, *image.Image, error) {
	baseImg, err := getBaseBuilderImg(ctx, client)
	if err != nil {
		return nil, nil, err
	}

	sOpt, err := frontend.SourceOptFromClient(ctx, client)
	if err != nil {
		return nil, nil, err
	}
	st, err := specToRpmLLB(spec, getDigestFromClientFn(ctx, client), baseImg, sOpt)
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

func getBaseBuilderImg(ctx context.Context, client gwclient.Client) (llb.State, error) {
	dc, err := dockerui.NewClient(client)
	if err != nil {
		return llb.Scratch(), err
	}

	// Check if the client passed in a named context for the toolkit.
	namedSt, cfg, err := dc.NamedContext(ctx, toolchainNamedContext, dockerui.ContextOpt{})
	if err != nil {
		return llb.Scratch(), err
	}

	if namedSt != nil {
		if cfg != nil {
			dt, err := json.Marshal(cfg)
			if err != nil {
				return llb.Scratch(), err
			}
			return namedSt.WithImageConfig(dt)
		}
		return *namedSt, nil
	}

	// See if there is a named context using the toolchain image ref
	namedSt, cfg, err = dc.NamedContext(ctx, toolchainImgRef, dockerui.ContextOpt{})
	if err != nil {
		return llb.Scratch(), err
	}

	if namedSt != nil {
		if cfg != nil {
			dt, err := json.Marshal(cfg)
			if err != nil {
				return llb.Scratch(), err
			}
			return namedSt.WithImageConfig(dt)
		}
		return *namedSt, nil
	}

	return llb.Image(toolchainImgRef, llb.WithMetaResolver(client)), nil
}

const cleanupScript = `
#!/usr/bin/env sh
for i in "${CHROOT_DIR}/"*; do
(
	if [  -d "${i}" ]; then
		cd "${i}"; find . ! -path "./upstream-cached-rpms/*" ! -path "./upstream-cached-rpms" ! -path "." -delete -print || exit 42
	fi
)
done
`

func getAllDeps(spec *dalec.Spec) []string {
	return sort.StringSlice(append(getBuildDeps(spec), getRuntimeDeps(spec)...))
}

func specToRpmLLB(spec *dalec.Spec, getDigest getDigestFunc, baseImg llb.State, sOpt dalec.SourceOpts) (llb.State, error) {
	br, err := spec2ToolkitRootLLB(spec, getDigest, sOpt)
	if err != nil {
		return llb.Scratch(), err
	}

	specsDir := "/build/SPECS"

	chrootDir := "/tmp/chroot"
	work := baseImg.
		// /.dockerenv is used by the toolkit to detect it's running in a container.
		// This makes the toolkit use a different strategy for setting up chroots.
		// Namely, it makes so the toolkit won't use "mount" to mount the stuff into the chroot which requires CAP_SYS_ADMIN.
		// (CAP_SYS_ADMIN is not enabled in our build).
		File(llb.Mkfile("/.dockerenv", 0o600, []byte{})).
		Dir("/build/toolkit").
		AddEnv("SPECS_DIR", specsDir).
		AddEnv("CONFIG_FILE", ""). // This is needed for VM images(?), don't need this for our case anyway and the default value is wrong for us.
		AddEnv("OUT_DIR", "/build/out").
		AddEnv("LOG_LEVEL", "debug").
		AddEnv("CACHED_RPMS_DIR", tookitRpmsCacheDir).
		AddEnv("CHROOT_DIR", chrootDir)

	specsMount := llb.AddMount(specsDir, br, llb.SourcePath("/SPECS"))

	// The actual rpm cache is stored under `./cache` in the cache dir.
	// This is the dir we need to mount.
	cachedRpmsDir := filepath.Join(tookitRpmsCacheDir, "cache")
	// Use a persistent cache for the cached rpms so that the same dir gets
	// bind-mounted everywhere its needed instead of each mount getting an
	// overlay causing inconsistencies between mounts.
	rpmsPeristCache := llb.AsPersistentCacheDir(identity.NewID(), llb.CacheMountShared)
	mainCachedRpmsMount := llb.AddMount(cachedRpmsDir, llb.Scratch(), rpmsPeristCache)

	// The mariner toolkit is trying to resolve *all* dependencies and not just build dependencies.
	// We need to make sure we have all the dependencies cached otherwise the build will fail.
	if deps := getAllDeps(spec); len(deps) > 0 {
		depsFile := llb.Scratch().File(llb.Mkfile("deps", 0o644, []byte(strings.Join(deps, "\n"))))
		// Use while loop with each package on a line in case there are too many packages to fit in a single command.
		dlCmd := `set -x; while read -r pkg; do tdnf install -y --alldeps --downloadonly --releasever=2.0 --downloaddir ` + cachedRpmsDir + ` ${pkg}; done < /tmp/deps`
		work.Run(
			shArgs(dlCmd),
			marinerTdnfCache,
			llb.AddMount("/tmp/deps", depsFile, llb.SourcePath("deps")),
			mainCachedRpmsMount,
		)
	}

	withCachedRpmsMounts := runOptFunc(func(ei *llb.ExecInfo) {
		mainCachedRpmsMount.SetRunOption(ei)

		// Mount cached packages into the chroot dirs so they are available to the chrooted build.
		// The toolchain has built-in (yum) repo files that points to "/upstream-cached-rpms",
		// so "tdnf install" as performed by the toolchain will read files from this location in the chroot.
		//
		// The toolchain can also run things in parallel so it will use multiple chroots.
		// See https://github.com/microsoft/CBL-Mariner/blob/8b1db59e9b011798e8e7750907f58b1bc9577da7/toolkit/tools/internal/buildpipeline/buildpipeline.go#L37-L117 for implementation of this.
		//
		// This is needed because the toolkit cannot mount anything into the chroot as it doesn't have CAP_SYS_ADMIN in our build.
		// So we have to mount the cached packages into the chroot dirs ourselves.
		dir := func(i int, p string) string {
			return filepath.Join(chrootDir, "dalec"+strconv.Itoa(i), p)
		}
		for i := 0; i < runtime.NumCPU(); i++ {
			llb.AddMount(dir(i, "upstream-cached-rpms"), llb.Scratch(), rpmsPeristCache).SetRunOption(ei)
		}
	})

	cleanupScript := llb.Scratch().File(llb.Mkfile("chroot-cleanup.sh", 0o755, []byte(cleanupScript)))
	buildCmd := `trap '/tmp/chroot-cleanup.sh > /dev/null' EXIT; make -j` + strconv.Itoa(runtime.NumCPU()) + ` build-packages || (set -x; ls -lh ` + cachedRpmsDir + `; cat /build/build/logs/pkggen/rpmbuilding/*; ls -lh ${CACHED_RPMS_DIR}/cache; exit 1)`

	worker := work.
		Run(shArgs("rm -rf ${CHROOT_DIR}/dalec*")).
		Run(
			shArgs(buildCmd),
			specsMount,
			marinerTdnfCache,
			withCachedRpmsMounts,
			llb.AddEnv("VERSION", spec.Version),
			llb.AddEnv("BUILD_NUMBER", spec.Revision),
			llb.AddEnv("REFRESH_WORKER_CHROOT", "n"),
			llb.AddMount("/tmp/chroot-cleanup.sh", cleanupScript, llb.SourcePath("chroot-cleanup.sh")),
		)

	st := worker.
		AddMount("/build/out", llb.Scratch()).
		File(llb.Rm("images"))
	return st, nil
}

type runOptFunc func(*llb.ExecInfo)

func (f runOptFunc) SetRunOption(ei *llb.ExecInfo) {
	f(ei)
}
