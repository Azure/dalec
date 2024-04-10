package mariner2

import (
	"context"
	"errors"
	"slices"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/frontend/rpm"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	bktargets "github.com/moby/buildkit/frontend/subrequests/targets"
)

const (
	DefaultTargetKey = "mariner2"
)

func Handle(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	var mux frontend.BuildMux

	mux.Add("rpm", handleRPM, &bktargets.Target{
		Name:        "rpm",
		Description: "Builds an rpm and src.rpm for mariner2.",
	})

	mux.Add("rpm/debug", handleDebug, nil)

	mux.Add("container", handleContainer, &bktargets.Target{
		Name:        "container",
		Description: "Builds a container image for mariner2.",
		Default:     true,
	})

	mux.Add("container/depsonly", handleDepsOnly, &bktargets.Target{
		Name:        "container/depsonly",
		Description: "Builds a container image with only the runtime dependencies installed.",
	})

	return mux.Handle(ctx, client)
}

func handleDebug(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return rpm.HandleDebug(getSpecWorker)(ctx, client)
}

func getSpecWorker(resolver llb.ImageMetaResolver, spec *dalec.Spec, targetKey string, opts ...llb.ConstraintsOpt) (llb.State, error) {
	st := getWorkerImage(resolver, opts...)
	if spec.HasGomods() {
		deps := spec.GetBuildDeps(targetKey)
		hasGolang := func(s string) bool {
			return s == "golang" || s == "msft-golang"
		}

		if !slices.ContainsFunc(deps, hasGolang) {
			return llb.Scratch(), errors.New("spec contains go modules but does not have golang in build deps")
		}
		st = st.With(installBuildDeps(spec, targetKey, opts...))
	}
	return st, nil
}
