package rpmbundle

import (
	"context"
	"fmt"
	"strings"

	"github.com/azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/image"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
)

const (
	marinerRef = "mcr.microsoft.com/cbl-mariner/base/core:2.0"
)

var baseMarinerPackages = []string{
	"binutils",
	"bison",
	"ca-certificates",
	"curl",
	"gawk",
	"git",
	"glibc-devel",
	"kernel-headers",
	"make",
	"msft-golang",
	"python",
	"rpm",
	"rpm-build",
	"wget",
}

var marinerTdnfCache = llb.AddMount("/var/tdnf/cache", llb.Scratch(), llb.AsPersistentCacheDir("mariner2-tdnf-cache", llb.CacheMountLocked))

var marinerBase = llb.Image(marinerRef).
	Run(
		shArgs("tdnf install -y "+strings.Join(baseMarinerPackages, " ")),
		marinerTdnfCache,
	).
	State

const cachedToolkitRPMDir = "/root/.cache/mariner2-toolkit-rpm-cache"

var (
	goModCache            = llb.AddMount("/go/pkg/mod", llb.Scratch(), llb.AsPersistentCacheDir("go-pkg-mod", llb.CacheMountShared))
	goBuildCache          = llb.AddMount("/root/.cache/go-build", llb.Scratch(), llb.AsPersistentCacheDir("go-build-cache", llb.CacheMountShared))
	cachedToolkitRPMMount = llb.AddMount(cachedToolkitRPMDir, llb.Scratch(), llb.AsPersistentCacheDir("mariner2-toolkit-rpm-cache", llb.CacheMountLocked))
	cachedToolkitRPMEnv   = llb.AddEnv("CACHED_RPMS_DIR", cachedToolkitRPMDir)
)

func handleRPM(ctx context.Context, client gwclient.Client, spec *frontend.Spec) (gwclient.Reference, *image.Image, error) {
	cf := client.(reexecFrontend)
	localSt, err := cf.CurrentFrontend()
	if err != nil {
		return nil, nil, fmt.Errorf("could not get current frontend: %w", err)
	}
	caps := client.BuildOpts().LLBCaps
	noMerge := !caps.Contains(pb.CapMergeOp)

	st, err := specToRpmLLB(spec, localSt, noMerge)
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

func specToRpmLLB(spec *frontend.Spec, localSt *llb.State, noMerge bool) (llb.State, error) {
	specs, err := specToRpmSpecLLB(spec, llb.Scratch())
	if err != nil {
		return llb.Scratch(), err
	}

	sources, err := specToSourcesLLB(spec, localSt, noMerge, llb.Scratch(), "SPECS/"+spec.Name)
	if err != nil {
		return llb.Scratch(), err
	}

	// The mariner toolkit wants a signatures file in the spec dir (next to the spec file) that contains the sha256sum of all sources.
	sigBase := localSt.
		// Add these so we can get a clean diff against this state and just get the signatures file
		File(llb.Mkdir("/tmp/SPECS", 0755, llb.WithParents(true))).
		File(llb.Mkdir("/SPECS/"+spec.Name, 0755, llb.WithParents(true)))

	sigSt := sigBase.Run(
		frontendCmd("signatures", "/tmp/SPECS/"+spec.Name, "/SPECS/"+spec.Name+"/"+spec.Name+".signatures.json"),
		llb.AddMount("/tmp/SPECS", sources, llb.Readonly, llb.SourcePath("SPECS")), // this *should* only include the sources files we care about, so our cmd can just itterate that directory.
	).State

	br := llb.Merge([]llb.State{
		llb.Scratch(),
		llb.Diff(llb.Scratch(), specs),
		llb.Diff(llb.Scratch(), sources),
		llb.Diff(sigBase, sigSt)},
	)

	const toolkitDir = "/usr/local/toolkit"
	toolkitMount := llb.AddMount(toolkitDir, marinerToolkit())

	st := marinerBase.Run(
		shArgs("make -j$(nproc) -C "+toolkitDir+" toolchain chroot-tools"),
		withRunMarinerChrootCache(),
		llb.AddEnv("REBUILD_TOOLS", "y"),
		llb.AddEnv("OUT_DIR", "/build/out"),
		llb.AddEnv("PROJECT_DIR", "/build/project"),
		toolkitMount,
		cachedToolkitRPMMount,
		cachedToolkitRPMEnv,
		goBuildCache,
		goModCache,
	).
		Run(
			shArgs("make -j$(nproc) -C /usr/local/toolkit build-packages || (cat /usr/local/build/logs/pkggen/rpmbuilding/*; exit 1)"),
			withRunMarinerChrootCache(),
			withRunMarinerPkgBuildCache(),
			llb.AddMount("/build/rpmbuild/SPECS", br, llb.SourcePath("/SPECS")),
			llb.AddEnv("SPECS_DIR", "/build/rpmbuild/SPECS"),
			llb.AddEnv("OUT_DIR", "/build/out"),
			llb.AddEnv("PROJECT_DIR", "/build/project"),
			llb.AddEnv("VERSION", spec.Version),
			llb.AddEnv("BUILD_NUMBER", spec.Revision),
			llb.AddEnv("REFRESH_WORKER_CHROOT", "n"),
			llb.Security(pb.SecurityMode_INSECURE),
			toolkitMount,
			cachedToolkitRPMMount,
			cachedToolkitRPMEnv,
			goBuildCache,
			goModCache,
		).State

	return llb.Scratch().File(
		llb.Copy(st, "/build/out", "/", frontend.WithDirContentsOnly(), frontend.WithIncludes([]string{"RPMS", "SRPMS"})),
	), nil
}

func marinerToolkit() llb.State {
	remote := llb.Git("https://github.com/microsoft/CBL-Mariner.git", "f3fee7cccffb21f1d7abf5ff940ba7db599fd4a2", llb.KeepGitDir())

	st := marinerBase.Dir("/build").
		Run(
			shArgs("cd toolkit; make package-toolkit REBUILD_TOOLS=y && mkdir -p /tmp/toolkit && tar -C /tmp/toolkit --strip-components=1 -zxf /build/out/toolkit-*.tar.gz"),
			llb.Security(pb.SecurityMode_INSECURE), // building the toolkit does mounts and other things that require root
			llb.AddMount("/build", remote),
			cachedToolkitRPMMount,
			cachedToolkitRPMEnv,
			goModCache,
			goBuildCache,
		).
		State

	return llb.Scratch().File(
		llb.Copy(st, "/tmp/toolkit", "/",
			frontend.WithDirContentsOnly(),
		),
	)
}

func setMarinerChrootCache(es *llb.ExecInfo) {
	es.State = es.State.With(
		llb.AddEnv("CHROOT_DIR", "/tmp/chroot"),
	)

	llb.AddMount("/tmp/chroot", llb.Scratch(), llb.AsPersistentCacheDir("mariner2-chroot-cache", llb.CacheMountLocked)).SetRunOption(es)
}

func setMarinerPkgBuildCache(es *llb.ExecInfo) {
	es.State = es.State.With(
		llb.AddEnv("PKGBUILD_DIR", "/tmp/pkg_build_dir"),
	)
	llb.AddMount("/tmp/pkg_build_dir", llb.Scratch(), llb.AsPersistentCacheDir("mariner2-pkgbuild-cache", llb.CacheMountLocked)).SetRunOption(es)
}

func withRunMarinerChrootCache() llb.RunOption {
	return runOptionFunc(setMarinerChrootCache)
}

func withRunMarinerPkgBuildCache() llb.RunOption {
	return runOptionFunc(setMarinerPkgBuildCache)
}

type runOptionFunc func(es *llb.ExecInfo)

func (f runOptionFunc) SetRunOption(es *llb.ExecInfo) {
	f(es)
}
