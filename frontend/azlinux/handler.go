package azlinux

import (
	"context"
	"errors"
	"slices"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/frontend/rpm"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests/targets"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	tdnfCacheDir = "/var/cache/tdnf"
)

type installFunc func(context.Context, gwclient.Client, dalec.SourceOpts) (llb.RunOption, error)

type worker interface {
	Base(sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, error)
	Install(pkgs []string, opts ...installOpt) llb.RunOption
	DefaultImageConfig(context.Context, llb.ImageMetaResolver, *ocispecs.Platform) (*dalec.DockerImageSpec, error)
	BasePackages() []string
}

func newHandler(w worker) gwclient.BuildFunc {
	var mux frontend.BuildMux

	mux.Add("rpm", handleRPM(w), &targets.Target{
		Name:        "rpm",
		Description: "Builds an rpm and src.rpm.",
	})
	mux.Add("rpm/debug", handleDebug(w), nil)

	mux.Add("container", handleContainer(w), &targets.Target{
		Name:        "container",
		Description: "Builds a container image for",
		Default:     true,
	})

	mux.Add("container/depsonly", handleDepsOnly(w), &targets.Target{
		Name:        "container/depsonly",
		Description: "Builds a container image with only the runtime dependencies installed.",
	})

	mux.Add("worker", handleBaseImg(w), &targets.Target{
		Name:        "worker",
		Description: "Builds the base worker image responsible for building the rpm",
	})

	return mux.Handle
}

func handleDebug(w worker) gwclient.BuildFunc {
	return func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
		sOpt, err := frontend.SourceOptFromClient(ctx, client)
		if err != nil {
			return nil, err
		}
		return rpm.HandleDebug(getSpecWorker(ctx, w, client, sOpt))(ctx, client)
	}
}

func getSpecWorker(ctx context.Context, w worker, client gwclient.Client, sOpt dalec.SourceOpts) rpm.WorkerFunc {
	return func(resolver llb.ImageMetaResolver, spec *dalec.Spec, targetKey string, opts ...llb.ConstraintsOpt) (llb.State, error) {
		st, err := w.Base(sOpt, opts...)
		if err != nil {
			return llb.Scratch(), err
		}
		if spec.HasGomods() {
			deps := dalec.SortMapKeys(spec.GetBuildDeps(targetKey))

			hasGolang := func(s string) bool {
				return s == "golang" || s == "msft-golang"
			}

			if !slices.ContainsFunc(deps, hasGolang) {
				return llb.Scratch(), errors.New("spec contains go modules but does not have golang in build deps")
			}

			installOpt, err := installBuildDeps(ctx, w, client, spec, targetKey, opts...)
			if err != nil {
				return llb.Scratch(), err
			}

			st = st.With(installOpt)
		}
		return st, nil
	}
}

func handleBaseImg(w worker) gwclient.BuildFunc {
	return func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
		return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {

			sOpt, err := frontend.SourceOptFromClient(ctx, client)
			if err != nil {
				return nil, nil, err
			}

			st, err := w.Base(sOpt)
			if err != nil {
				return nil, nil, err
			}

			def, err := st.Marshal(ctx)
			if err != nil {
				return nil, nil, err
			}

			req := gwclient.SolveRequest{
				Definition: def.ToPB(),
			}

			res, err := client.Solve(ctx, req)
			if err != nil {
				return nil, nil, err
			}

			ref, err := res.SingleRef()
			if err != nil {
				return nil, nil, err
			}

			cfg, err := w.DefaultImageConfig(ctx, client, platform)
			if err != nil {
				return nil, nil, err
			}

			return ref, cfg, nil
		})
	}
}
