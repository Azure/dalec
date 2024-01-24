package fixtures

import (
	"context"
	"encoding/json"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	"github.com/moby/buildkit/exporter/containerimage/image"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

var (
	goModCache   = llb.AddMount("/go/pkg/mod", llb.Scratch(), llb.AsPersistentCacheDir("dalec-go-mod-cache", llb.CacheMountShared))
	goBuildCache = llb.AddMount("/root/.cache/go-build", llb.Scratch(), llb.AsPersistentCacheDir("dalec-go-build-cache", llb.CacheMountShared))
)

func PhonyFrontend(ctx context.Context, gwc gwclient.Client) (*gwclient.Result, error) {
	dc, err := dockerui.NewClient(gwc)
	if err != nil {
		return nil, err
	}

	bctx, err := dc.MainContext(ctx)
	if err != nil {
		return nil, err
	}

	st := llb.Image("golang:1.21", llb.WithMetaResolver(gwc)).
		Run(
			llb.Args([]string{"go", "build", "-o=/build/out", "./test/fixtures/phony"}),
			llb.AddEnv("CGO_ENABLED", "0"),
			goModCache,
			goBuildCache,
			llb.Dir("/build/src"),
			llb.AddMount("/build/src", *bctx, llb.Readonly),
		).
		AddMount("/build/out", llb.Scratch())

	cfg := image.Image{
		Config: image.ImageConfig{
			ImageConfig: ocispecs.ImageConfig{
				Entrypoint: []string{"/phony"},
			},
		},
	}
	injectFrontendLabels(&cfg)

	dt, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}

	def, err := st.Marshal(ctx)
	if err != nil {
		return nil, err
	}

	res, err := gwc.Solve(ctx, gwclient.SolveRequest{
		Definition: def.ToPB(),
	})
	if err != nil {
		return nil, err
	}

	res.AddMeta(exptypes.ExporterImageConfigKey, dt)

	return res, nil
}

func injectFrontendLabels(cfg *image.Image) {
	if cfg.Config.Labels == nil {
		cfg.Config.Labels = map[string]string{}
	}

	cfg.Config.Labels["moby.buildkit.frontend.network.none"] = "true"
	cfg.Config.Labels["moby.buildkit.frontend.caps"] = "moby.buildkit.frontend.inputs,moby.buildkit.frontend.subrequests,moby.buildkit.frontend.contexts"
}
