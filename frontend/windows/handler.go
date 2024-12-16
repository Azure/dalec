package windows

import (
	"context"
	"encoding/json"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/frontend/deb/distro"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	bktargets "github.com/moby/buildkit/frontend/subrequests/targets"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	outputKey                     = "windows"
	workerImgRef                  = "mcr.microsoft.com/mirror/docker/library/ubuntu:jammy"
	WindowscrossWorkerContextName = "dalec-windowscross-worker"
)

var (
	distroConfig = &distro.Config{
		ImageRef:       workerImgRef,
		AptCachePrefix: aptCachePrefix,
		VersionID:      "ubuntu22.04",
		ContextRef:     WindowscrossWorkerContextName,
		BuilderPackages: []string{
			"aptitude",
			"build-essential",
			"binutils-mingw-w64",
			"g++-mingw-w64-x86-64",
			"gcc",
			"git",
			"make",
			"pkg-config",
			"zip",
			"aptitude",
			"dpkg-dev",
			"debhelper",
		},
	}
)

var (
	windowscross_1809     = &config{baseImage: "mcr.microsoft.com/windows/nanoserver:1809"}
	windowscross_ltsc2019 = &config{baseImage: "mcr.microsoft.com/windows/nanoserver:ltsc2019"}
	windowscross_ltsc2022 = &config{baseImage: "mcr.microsoft.com/windows/nanoserver:ltsc2022"}
	windowscross_ltsc2025 = &config{baseImage: "mcr.microsoft.com/windows/nanoserver:ltsc2025"}
	windowscross_20H2     = &config{baseImage: "mcr.microsoft.com/windows/nanoserver:20H2"}
	windowscross_1909     = &config{baseImage: "mcr.microsoft.com/windows/nanoserver:1909"}
	windowscross_2004     = &config{baseImage: "mcr.microsoft.com/windows/nanoserver:2004"}
)

func Handlers(ctx context.Context, client gwclient.Client, m *frontend.BuildMux) error {
	targets := map[string]gwclient.BuildFunc{
		"windowscross-1809":     windowscross_1809.Handle,
		"windowscross-ltsc2019": windowscross_ltsc2019.Handle,
		"windowscross-ltsc2022": windowscross_ltsc2022.Handle,
		"windowscross-ltsc2025": windowscross_ltsc2025.Handle,
		"windowscross-20H2":     windowscross_20H2.Handle,
		"windowscross-1909":     windowscross_1909.Handle,
		"windowscross-2004":     windowscross_2004.Handle,
	}
	return frontend.LoadBuiltinTargets(targets)(ctx, client, m)
}

type config struct {
	baseImage string
}

func (c *config) Handle(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	var mux frontend.BuildMux

	mux.Add("zip", handleZip, &bktargets.Target{
		Name:        "zip",
		Description: "Builds binaries combined into a zip file",
	})

	mux.Add("container", c.handleContainer, &bktargets.Target{
		Name:        "container",
		Description: "Builds binaries and installs them into a Windows base image",
		Default:     true,
	})

	mux.Add("worker", handleWorker, &bktargets.Target{
		Name:        "worker",
		Description: "Builds the base worker image responsible for building the package",
	})

	return mux.Handle(ctx, client)
}

func handleWorker(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		sOpt, err := frontend.SourceOptFromClient(ctx, client)
		if err != nil {
			return nil, nil, err
		}

		var opts []llb.ConstraintsOpt
		if platform != nil {
			opts = append(opts, llb.Platform(*platform))
		}

		st, err := distroConfig.Worker(sOpt, opts...)
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

		_, _, dt, err := client.ResolveImageConfig(ctx, workerImgRef, sourceresolver.Opt{
			Platform: platform,
		})
		if err != nil {
			return nil, nil, err
		}

		var img dalec.DockerImageSpec
		if err := json.Unmarshal(dt, &img); err != nil {
			return nil, nil, err
		}

		ref, err := res.SingleRef()
		if err != nil {
			return nil, nil, err
		}

		return ref, &img, nil
	})
}
