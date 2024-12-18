package frontend

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/Azure/dalec"
	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

type LoadConfig struct {
	SubstituteOpts []dalec.SubstituteOpt
}

type LoadOpt func(*LoadConfig)

func WithAllowArgs(args ...string) LoadOpt {
	return func(cfg *LoadConfig) {
		set := make(map[string]struct{}, len(args))
		for _, arg := range args {
			set[arg] = struct{}{}
		}
		cfg.SubstituteOpts = append(cfg.SubstituteOpts, func(cfg *dalec.SubstituteConfig) {
			orig := cfg.AllowArg

			cfg.AllowArg = func(key string) bool {
				if orig != nil && orig(key) {
					return true
				}
				_, ok := set[key]
				return ok
			}
		})
	}
}

func LoadSpec(ctx context.Context, client *dockerui.Client, platform *ocispecs.Platform, opts ...LoadOpt) (*dalec.Spec, error) {
	cfg := LoadConfig{}

	for _, o := range opts {
		o(&cfg)
	}

	src, err := client.ReadEntrypoint(ctx, "Dockerfile")
	if err != nil {
		return nil, fmt.Errorf("could not read spec file: %w", err)
	}

	spec, err := dalec.LoadSpec(bytes.TrimSpace(src.Data))
	if err != nil {
		return nil, fmt.Errorf("error loading spec: %w", err)
	}

	args := dalec.DuplicateMap(client.BuildArgs)
	if platform == nil {
		p := platforms.DefaultSpec()
		platform = &p
	}

	fillPlatformArgs("TARGET", args, *platform)
	fillPlatformArgs("BUILD", args, client.BuildPlatforms[0])

	if err := spec.SubstituteArgs(args, cfg.SubstituteOpts...); err != nil {
		return nil, errors.Wrap(err, "error resolving build args")
	}
	return spec, nil
}

func getOS(platform ocispecs.Platform) string {
	return platform.OS
}

func getArch(platform ocispecs.Platform) string {
	return platform.Architecture
}

func getVariant(platform ocispecs.Platform) string {
	return platform.Variant
}

func getPlatformFormat(platform ocispecs.Platform) string {
	return platforms.Format(platform)
}

var passthroughGetters = map[string]func(ocispecs.Platform) string{
	"OS":       getOS,
	"ARCH":     getArch,
	"VARIANT":  getVariant,
	"PLATFORM": getPlatformFormat,
}

func fillPlatformArgs(prefix string, args map[string]string, platform ocispecs.Platform) {
	for attr, getter := range passthroughGetters {
		args[prefix+attr] = getter(platform)
	}
}

type PlatformBuildFunc func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error)

// BuildWithPlatform is a helper function to build a spec with a given platform
// It takes care of looping through each tarrget platform and executing the build with the platform args substituted in the spec.
// This also deals with the docker-style multi-platform output.
func BuildWithPlatform(ctx context.Context, client gwclient.Client, f PlatformBuildFunc) (*gwclient.Result, error) {
	dc, err := dockerui.NewClient(client)
	if err != nil {
		return nil, err
	}

	rb, err := dc.Build(ctx, func(ctx context.Context, platform *ocispecs.Platform, idx int) (gwclient.Reference, *dalec.DockerImageSpec, *dalec.DockerImageSpec, error) {
		spec, err := LoadSpec(ctx, dc, platform)
		if err != nil {
			return nil, nil, nil, err
		}
		targetKey := GetTargetKey(dc)

		ref, cfg, err := f(ctx, client, platform, spec, targetKey)
		if cfg != nil {
			now := time.Now()
			cfg.Created = &now
		}
		return ref, cfg, nil, err
	})
	if err != nil {
		return nil, err
	}
	return rb.Finalize()
}

// GetBaseImage returns an image that first checks if the client provided the
// image in the build context matching the image ref.
//
// This follows the behavior of of the dockerfile frontend.
func GetBaseImage(sOpt dalec.SourceOpts, ref string) llb.State {
	return llb.Scratch().Async(func(ctx context.Context, _ llb.State, c *llb.Constraints) (llb.State, error) {
		fromClient, err := sOpt.GetContext(ref, dalec.WithConstraint(c))
		if err != nil {
			return llb.Scratch(), err
		}

		if fromClient != nil {
			return *fromClient, nil
		}
		return llb.Image(ref, llb.WithMetaResolver(sOpt.Resolver), dalec.WithConstraint(c)), nil
	})
}
