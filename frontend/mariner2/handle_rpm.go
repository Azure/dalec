package mariner2

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/frontend/rpm"
	"github.com/moby/buildkit/client/llb"
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

func handleRPM(ctx context.Context, client gwclient.Client, spec *dalec.Spec) (gwclient.Reference, *dalec.DockerImageSpec, error) {
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
	return ref, &dalec.DockerImageSpec{}, err
}

func shArgs(cmd string) llb.RunOption {
	return llb.Args([]string{"sh", "-c", cmd})
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
		deps := spec.GetBuildDeps(targetKey)
		if len(deps) == 0 {
			return in
		}
		opts = append(opts, dalec.ProgressGroup("Install build deps"))

		return in.
			Run(
				shArgs(fmt.Sprintf("tdnf install --releasever=2.0 -y %s", strings.Join(deps, " "))),
				defaultTndfCacheMount(),
				dalec.WithConstraints(opts...),
			).
			Root()
	}
}

func specToRpmLLB(spec *dalec.Spec, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, error) {
	br, err := rpm.SpecToBuildrootLLB(spec, targetKey, sOpt, opts...)
	if err != nil {
		return llb.Scratch(), err
	}
	specPath := filepath.Join("SPECS", spec.Name, spec.Name+".spec")

	base := getWorkerImage(sOpt, opts...).With(installBuildDeps(spec, opts...))
	return rpm.Build(br, base, specPath, opts...), nil
}
