package jammy

import (
	"context"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/frontend/deb"
	"github.com/moby/buildkit/client/llb"
	"golang.org/x/exp/maps"
)

var (
	varCacheAptMount = llb.AddMount("/var/cache/apt", llb.Scratch(), llb.AsPersistentCacheDir("dalec-jammy-var-cache-apt", llb.CacheMountLocked))
	varLibAptMount   = llb.AddMount("/var/lib/apt", llb.Scratch(), llb.AsPersistentCacheDir("dalec-jammy-var-lib-apt", llb.CacheMountLocked))
)

func shArgs(args string) llb.RunOption {
	return llb.Args(append([]string{"sh", "-c"}, args))
}

func handleDeb(ctx context.Context, client frontend.Client, spec *dalec.Spec) (frontend.Reference, *frontend.Image, error) {
	sOpt, err := frontend.SourceOptFromClient(ctx, client)
	if err != nil {
		return nil, nil, err
	}

	st, err := BuildDeb(spec, sOpt)
	if err != nil {
		return nil, nil, err
	}

	def, err := st.Marshal(ctx)
	if err != nil {
		return nil, nil, err
	}

	res, err := client.Solve(ctx, frontend.SolveRequest{
		Definition: def.ToPB(),
	})
	if err != nil {
		return nil, nil, err
	}

	ref, err := res.SingleRef()
	if err != nil {
		return nil, nil, err
	}
	return ref, &frontend.Image{}, nil
}

func buildDeps(spec *dalec.Spec) []string {
	deps := dalec.GetDeps(spec, targetKey)
	ls := maps.Keys(deps.Build)
	slices.Sort(ls)

	return ls
}

func BuildDeb(spec *dalec.Spec, sOpt dalec.SourceOpts) (llb.State, error) {
	base := workerImg(sOpt)

	dr, err := deb.Debroot(spec, llb.Scratch(), targetKey, "")
	if err != nil {
		return llb.Scratch(), err
	}
	sources, err := dalec.Sources(spec, sOpt)
	if err != nil {
		return llb.Scratch(), err
	}

	work := base.
		Run(
			shArgs("apt-get update && apt-get install -y "+strings.Join(buildDeps(spec), " ")),
			varCacheAptMount,
			varLibAptMount,
		).
		Run(
			shArgs("set -e; cd pkg; dpkg-buildpackage -us -uc -b && mkdir -p /tmp/out && mv ../*.deb /tmp/out/"),
			llb.Dir("/work"),
			llb.AddMount("/work/pkg", dr),
			dalec.RunOptFunc(func(ei *llb.ExecInfo) {
				for name, src := range sources {
					llb.AddMount(filepath.Join("/work/pkg", name), src).SetRunOption(ei)
				}
			}),
		)

	st := work.AddMount("/tmp/out", llb.Scratch())

	return st, nil
}

func workerImg(sOpt dalec.SourceOpts) llb.State {
	// TODO: support named context override... also this should possibly be its own image, maybe?
	return llb.Image("ubuntu:jammy", llb.WithMetaResolver(sOpt.Resolver)).
		Run(
			shArgs("apt-get update && apt-get install -y build-essential dh-make equivs fakeroot dh-apparmor"),
			varCacheAptMount,
			varLibAptMount,
		).Root()
}
