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

type worker interface {
	Base(resolver llb.ImageMetaResolver, opts ...llb.ConstraintsOpt) llb.State
	Install(pkgs []string, opts ...installOpt) llb.RunOption
	DefaultImageConfig(context.Context, llb.ImageMetaResolver, *ocispecs.Platform) (*dalec.DockerImageSpec, error)
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

	return mux.Handle
}

func handleDebug(w worker) gwclient.BuildFunc {
	return func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
		return rpm.HandleDebug(getSpecWorker(w))(ctx, client)
	}
}

func getSpecWorker(w worker) rpm.WorkerFunc {
	return func(resolver llb.ImageMetaResolver, spec *dalec.Spec, targetKey string, opts ...llb.ConstraintsOpt) (llb.State, error) {
		st := w.Base(resolver, opts...)
		if spec.HasGomods() {
			deps := spec.GetBuildDeps(targetKey)
			hasGolang := func(s string) bool {
				return s == "golang" || s == "msft-golang"
			}

			if !slices.ContainsFunc(deps, hasGolang) {
				return llb.Scratch(), errors.New("spec contains go modules but does not have golang in build deps")
			}
			st = st.With(installBuildDeps(w, spec, targetKey, opts...))
		}
		return st, nil
	}
}
