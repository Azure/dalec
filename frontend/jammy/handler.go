package jammy

import (
	"context"
	"encoding/json"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/frontend/deb"
	"github.com/Azure/dalec/frontend/deb/distro"
	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests/targets"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	DefaultTargetKey = "jammy"
	AptCachePrefix   = "jammy"

	jammyRef  = "mcr.microsoft.com/mirror/docker/library/ubuntu:jammy"
	versionID = "ubuntu22.04"
)

var (
	distroConfig = &distro.Config{
		ImageRef:       jammyRef,
		AptCachePrefix: AptCachePrefix,
		VersionID:      versionID,
		ContextRef:     JammyWorkerContextName,
		BuilderPackages: []string{
			"aptitude",
			"dpkg-dev",
			"devscripts",
			"equivs",
			"fakeroot",
			"dh-make",
			"build-essential",
			"dh-apparmor",
			"dh-make",
			"dh-exec",
			"debhelper-compat=" + deb.DebHelperCompat,
		},
	}
)

func Handle(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	var mux frontend.BuildMux

	mux.Add("deb", handleDeb, &targets.Target{
		Name:        "deb",
		Description: "Builds a deb package for jammy.",
		Default:     true,
	})

	mux.Add("testing/container", handleContainer, &targets.Target{
		Name:        "testing/container",
		Description: "Builds a container image for jammy for testing purposes only.",
	})

	mux.Add("dsc", handleDebianSourcePackage, &targets.Target{
		Name:        "dsc",
		Description: "Builds a Debian source package for jammy.",
	})

	mux.Add("worker", handleWorker, &targets.Target{
		Name:        "worker",
		Description: "Builds the worker image.",
	})

	return mux.Handle(ctx, client)
}

func handleWorker(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		sOpt, err := frontend.SourceOptFromClient(ctx, client)
		if err != nil {
			return nil, nil, err
		}

		p := platforms.DefaultSpec()
		if platform != nil {
			p = *platform
		}
		pOpt := llb.Platform(p)

		st, err := distroConfig.Worker(sOpt, pOpt)
		if err != nil {
			return nil, nil, err
		}

		def, err := st.Marshal(ctx)
		if err != nil {
			return nil, nil, err
		}

		res, err := client.Solve(ctx, gwclient.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, nil, err
		}

		ref, err := res.SingleRef()
		if err != nil {
			return nil, nil, err
		}

		_, _, dt, err := client.ResolveImageConfig(ctx, jammyRef, sourceresolver.Opt{
			Platform: platform,
		})
		if err != nil {
			return nil, nil, err
		}

		var cfg dalec.DockerImageSpec
		if err := json.Unmarshal(dt, &cfg); err != nil {
			return nil, nil, err
		}
		return ref, &cfg, nil
	})
}
