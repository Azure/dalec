package frontend

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Azure/dalec"
	"github.com/containerd/containerd/platforms"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

type imageBuilderConfig struct {
	platform *ocispecs.Platform
}

type ConfigOpt func(*imageBuilderConfig)

func WithPlatform(p ocispecs.Platform) ConfigOpt {
	return func(c *imageBuilderConfig) {
		c.platform = &p
	}
}

func BuildImageConfig(ctx context.Context, client gwclient.Client, spec *dalec.Spec, targetKey string, dflt string, opts ...ConfigOpt) (*dalec.DockerImageSpec, error) {
	dc, err := dockerui.NewClient(client)
	if err != nil {
		return nil, err
	}

	builderCfg := imageBuilderConfig{}
	for _, optFunc := range opts {
		optFunc(&builderCfg)
	}

	baseImgRef := getBaseOutputImage(spec, targetKey, dflt)
	platform := platforms.DefaultSpec()
	if builderCfg.platform != nil {
		platform = *builderCfg.platform
	}

	_, _, dt, err := client.ResolveImageConfig(ctx, baseImgRef, sourceresolver.Opt{
		Platform: &platform,
		ImageOpt: &sourceresolver.ResolveImageOpt{
			ResolveMode: dc.ImageResolveMode.String(),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("error resolving image config: %w", err)
	}

	var img dalec.DockerImageSpec
	if err := json.Unmarshal(dt, &img); err != nil {
		return nil, fmt.Errorf("error unmarshalling image config: %w", err)
	}

	cfg := img.Config
	if err := dalec.MergeImageConfig(&cfg, mergeSpecImage(spec, targetKey)); err != nil {
		return nil, err
	}

	img.Config = cfg
	return &img, nil
}

func getBaseOutputImage(spec *dalec.Spec, target, defaultBase string) string {
	baseRef := defaultBase
	if spec.Targets[target].Image != nil && spec.Targets[target].Image.Base != "" {
		baseRef = spec.Targets[target].Image.Base
	}
	return baseRef
}

func mergeSpecImage(spec *dalec.Spec, target string) *dalec.ImageConfig {
	var cfg dalec.ImageConfig

	if spec.Image != nil {
		cfg = *spec.Image
	}

	if i := spec.Targets[target].Image; i != nil {
		if i.Entrypoint != "" {
			cfg.Entrypoint = i.Entrypoint
		}

		if i.Cmd != "" {
			cfg.Cmd = i.Cmd
		}

		cfg.Env = append(cfg.Env, i.Env...)

		for k, v := range i.Volumes {
			cfg.Volumes[k] = v
		}

		for k, v := range i.Labels {
			cfg.Labels[k] = v
		}

		if i.WorkingDir != "" {
			cfg.WorkingDir = i.WorkingDir
		}

		if i.StopSignal != "" {
			cfg.StopSignal = i.StopSignal
		}

		if i.Base != "" {
			cfg.Base = i.Base
		}
	}

	return &cfg
}
