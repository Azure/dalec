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

func handleRPM(ctx context.Context, client gwclient.Client, spec *dalec.Spec) (gwclient.Reference, *image.Image, error) {
	if err := rpm.ValidateSpec(spec); err != nil {
		return nil, nil, fmt.Errorf("rpm: invalid spec: %w", err)
	}

	pg := dalec.ProgressGroup("Building mariner2 rpm: " + spec.Name)
	sOpt, err := frontend.SourceOptFromClient(ctx, client)
	if err != nil {
		return nil, nil, err
	}
	st, err := specToRpmLLB(spec, sOpt, pg)
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

func partitionBuildDeps(spec *dalec.Spec) (dalecDeps []string, repoDeps []string) {
	if spec.Dependencies == nil {
		return []string{}, []string{}
	}

	dalecDeps = make([]string, 0, len(spec.Dependencies.Build))
	repoDeps = make([]string, 0, len(spec.Dependencies.Build))

	buildDeps := getBuildDeps(spec)
	for _, dep := range buildDeps {
		if _, ok := dalec.BuildGraph.Get(dep); ok {
			dalecDeps = append(dalecDeps, dep)
			continue
		}

		repoDeps = append(repoDeps, dep)
	}

	return dalecDeps, repoDeps
}

func partitionRuntimeDeps(spec *dalec.Spec) (dalecDeps []string, repoDeps []string) {
	if spec.Dependencies == nil {
		return []string{}, []string{}
	}

	dalecDeps = make([]string, 0, len(spec.Dependencies.Runtime))
	repoDeps = make([]string, 0, len(spec.Dependencies.Runtime))

	buildDeps := getRuntimeDeps(spec)
	for _, dep := range buildDeps {
		if _, ok := dalec.BuildGraph.Get(dep); ok {
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

func installBuildDeps(spec *dalec.Spec, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		_, repoDeps := partitionBuildDeps(spec)
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

func installSpecLocalBuildDeps(spec *dalec.Spec, depRPMs llb.State, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		specLocalDeps, _ := partitionBuildDeps(spec)
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

// func installDalecBuildDeps(spec *dalec.Spec, deps map[string]llb.State, opts ...llb.ConstraintsOpt) llb.StateOption {
// 	return func(in llb.State) llb.State {
// 		opts = append(opts, dalec.ProgressGroup("Install dalec build deps"))
//         var es llb.ExecState
//         // ro := []llb.RunOption{
//         //     shArgs("tdnf install --releasever=2.0 -y "+dstPath),
//         //     defaultTndfCacheMount(),
//         //     dalec.WithConstraints(opts...),
//         // }
// 		for name, st := range deps {
// 			dstPath := filepath.Join("/tmp/dalec_rpms", name)
//             ro = append(ro, llb.AddMount())

// 			es = in.Run(
//                 llb.Copy(llb.)
// 				llb.AddMount(dstPath, st, llb.SourcePath("/RPMS")),
// 			)
// 		}

// 		return in.
// 			Run(
// 				shArgs(fmt.Sprintf("tdnf install --releasever=2.0 -y %s", strings.Join(deps, " "))),
// 			).
// 			Root()
// 	}
// }

// func specToRpmLLBWithDeps(spec *dalec.Spec, sOpt dalec.SourceOpts, deps map[string]llb.State, opts ...llb.ConstraintsOpt) (llb.State, error) {
// 	br, err := rpm.SpecToBuildrootLLB(spec, targetKey, sOpt, opts...)
// 	if err != nil {
// 		return llb.Scratch(), err
// 	}
// 	specPath := filepath.Join("SPECS", spec.Name, spec.Name+".spec")

// 	base := getWorkerImage(sOpt, opts...).With(installDalecBuildDeps(spec, deps, opts...))
// 	return rpm.Build(br, base, specPath, opts...), nil
// }

func specToRpmLLB(spec *dalec.Spec, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, error) {
	br, err := rpm.SpecToBuildrootLLB(spec, targetKey, sOpt, opts...)
	if err != nil {
		return llb.Scratch(), err
	}
	specPath := filepath.Join("SPECS", spec.Name, spec.Name+".spec")

	base := getWorkerImage(sOpt, opts...).With(installBuildDeps(spec, opts...))
	return rpm.Build(br, base, specPath, opts...), nil
}

type buildDeps struct {
	names []string
	rpms  llb.State
}

func specToRpmLLBWithBuildDeps(spec *dalec.Spec, sOpt dalec.SourceOpts, depRPMs llb.State, opts ...llb.ConstraintsOpt) (llb.State, error) {
	br, err := rpm.SpecToBuildrootLLB(spec, targetKey, sOpt, opts...)
	if err != nil {
		return llb.Scratch(), err
	}
	specPath := filepath.Join("SPECS", spec.Name, spec.Name+".spec")

	base := getWorkerImage(sOpt, opts...).With(installBuildDeps(spec, opts...)).With(installSpecLocalBuildDeps(spec, depRPMs, opts...))
	return rpm.Build(br, base, specPath, opts...), nil
}
