package mariner2

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/frontend/rpm"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/image"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

const (
	marinerRef = "mcr.microsoft.com/cbl-mariner/base/core:2.0"

	tdnfCacheDir  = "/var/cache/tdnf"
	tdnfCacheName = "mariner2-tdnf-cache"
)

func defaultTndfCacheMount() llb.RunOption {
	return tdnfCacheMountWithPrefix("")
}

func tdnfCacheMountWithPrefix(prefix string) llb.RunOption {
	// note: We are using CacheMountLocked here because without it, while there are concurrent builds happening, tdnf exits with a lock error.
	// dnf is supposed to wait for locks, but it seems like that is not happening with tdnf.
	return llb.AddMount(filepath.Join(prefix, tdnfCacheDir), llb.Scratch(), llb.AsPersistentCacheDir(tdnfCacheName, llb.CacheMountLocked))
}

func handleRPM(ctx context.Context, client gwclient.Client, graph *dalec.Graph) (gwclient.Reference, *image.Image, error) {
	spec := graph.Target()
	if err := rpm.ValidateSpec(&spec); err != nil {
		return nil, nil, fmt.Errorf("rpm: invalid spec: %w", err)
	}

	pg := dalec.ProgressGroup("Building mariner2 rpm: " + spec.Name)
	sOpt, err := frontend.SourceOptFromClient(ctx, client)
	if err != nil {
		return nil, nil, err
	}

	baseImg := getWorkerImage(sOpt, pg)
	rpmDirs, err := buildRPMDirs(graph, baseImg, sOpt, pg)
	if err != nil {
		return nil, nil, err
	}

	rpm, ok := rpmDirs[spec.Name]
	if !ok {
		return nil, nil, fmt.Errorf("graph error: llb for final rpm %q not found", spec.Name)
	}

	def, err := rpm.Marshal(ctx, pg)
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

// Partitions build deps into dalec spec-local build deps (first return value)
// and build deps that are expected to be found in the package repo (second
// return value).
func partitionBuildDeps(spec *dalec.Spec, graph *dalec.Graph) ([]string, []string) {
	buildDeps := getBuildDeps(spec)
	dalecDeps := make([]string, 0, len(buildDeps))
	repoDeps := make([]string, 0, len(buildDeps))

	for _, dep := range buildDeps {
		if _, ok := graph.Get(dep); ok {
			dalecDeps = append(dalecDeps, dep)
			continue
		}

		repoDeps = append(repoDeps, dep)
	}

	return dalecDeps, repoDeps
}

func getWorkerImage(sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) llb.State {
	opts = append(opts, dalec.ProgressGroup("Prepare worker image"))
	return llb.Image(marinerRef, llb.WithMetaResolver(sOpt.Resolver), dalec.WithConstraints(opts...)).
		Run(
			shArgs("tdnf install -y rpm-build mariner-rpm-macros build-essential"),
			defaultTndfCacheMount(),
			dalec.WithConstraints(opts...),
		).
		Root()
}

func installBuildDeps(repoDeps []string, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		if len(repoDeps) == 0 {
			return in
		}
		opts = append(opts, dalec.ProgressGroup("Install repo build deps"))

		return in.
			Run(
				shArgs(fmt.Sprintf("tdnf install --releasever=2.0 -y %s", strings.Join(repoDeps, " "))),
				defaultTndfCacheMount(),
				dalec.WithConstraints(opts...),
			).
			Root()
	}
}

func installSpecLocalBuildDeps(specLocalDeps []string, depRPMs llb.State, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		if len(specLocalDeps) == 0 {
			return in
		}
		opts = append(opts, dalec.ProgressGroup("Install spec-local build deps"))

		const depDir = "/tmp/builddeps"
		return in.
			Run(
				shArgs(`
                    find `+depDir+` -type f -name "*.rpm" -print0 |
                        xargs -0 tdnf install --releasever=2.0 --nogpgcheck --setopt=reposdir=/etc/yum.repos.d -y
                `),
				llb.AddMount(depDir, depRPMs),
				defaultTndfCacheMount(),
				dalec.WithConstraints(opts...),
			).
			Root()
	}
}

func specToRpmLLBWithBuildDeps(spec *dalec.Spec, dalecDeps []string, repoDeps []string, sOpt dalec.SourceOpts, depRPMs llb.State, opts ...llb.ConstraintsOpt) (llb.State, error) {
	br, err := rpm.SpecToBuildrootLLB(spec, targetKey, sOpt, opts...)
	if err != nil {
		return llb.Scratch(), err
	}
	specPath := filepath.Join("SPECS", spec.Name, spec.Name+".spec")

	base := getWorkerImage(sOpt, opts...).With(installBuildDeps(repoDeps, opts...)).With(installSpecLocalBuildDeps(dalecDeps, depRPMs, opts...))
	return rpm.Build(br, base, specPath, opts...), nil
}
