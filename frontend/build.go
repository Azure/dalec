package frontend

import (
	"bytes"
	"context"
	"fmt"

	"github.com/Azure/dalec"
	"github.com/containerd/containerd/platforms"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests/targets"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

func loadSpec(ctx context.Context, client *dockerui.Client) (*dalec.Spec, error) {
	src, err := client.ReadEntrypoint(ctx, "Dockerfile")
	if err != nil {
		return nil, fmt.Errorf("could not read spec file: %w", err)
	}

	spec, err := dalec.LoadSpec(bytes.TrimSpace(src.Data))
	if err != nil {
		return nil, fmt.Errorf("error loading spec: %w", err)
	}
	return spec, nil
}

func listBuildTargets(group string) []*targetWrapper {
	if group != "" {
		return registeredHandlers.GetGroup(group)
	}
	return registeredHandlers.All()
}

func lookupHandler(target string) (BuildFunc, error) {
	if target == "" {
		return registeredHandlers.Default().Build, nil
	}

	t := registeredHandlers.Get(target)
	if t == nil {
		return nil, fmt.Errorf("unknown target %q", target)
	}
	return t.Build, nil
}

func makeRequestHandler(target string) dockerui.RequestHandler {
	h := dockerui.RequestHandler{AllowOther: true}

	h.ListTargets = func(ctx context.Context) (*targets.List, error) {
		var ls targets.List
		for _, tw := range listBuildTargets(target) {
			ls.Targets = append(ls.Targets, tw.Target)
		}
		return &ls, nil
	}

	return h
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

// Build is the main entrypoint for the dalec frontend.
func Build(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	if !SupportsDiffMerge(client) {
		dalec.DisableDiffMerge(true)
	}

	bc, err := dockerui.NewClient(client)
	if err != nil {
		return nil, fmt.Errorf("could not create build client: %w", err)
	}

	spec, err := loadSpec(ctx, bc)
	if err != nil {
		return nil, err
	}

	if err := registerSpecHandlers(ctx, spec, client); err != nil {
		return nil, err
	}

	res, handled, err := bc.HandleSubrequest(ctx, makeRequestHandler(bc.Target))
	if err != nil || handled {
		return res, err
	}

	if !handled {
		// Handle additional subrequests supported by dalec
		requestID := client.BuildOpts().Opts[requestIDKey]
		switch requestID {
		case dalecSubrequstForwardBuild:
		case "":
		default:
			return nil, fmt.Errorf("unknown request id %q", requestID)
		}
	}

	f, err := lookupHandler(bc.Target)
	if err != nil {
		return nil, err
	}

	rb, err := bc.Build(ctx, func(ctx context.Context, platform *ocispecs.Platform, idx int) (r gwclient.Reference, img, baseImg *dockerspec.DockerOCIImage, err error) {
		var targetPlatform, buildPlatform ocispecs.Platform
		if platform != nil {
			targetPlatform = *platform
		} else {
			targetPlatform = platforms.DefaultSpec()
		}

		// the dockerui client, given the current implementation, should only ever have
		// a single build platform
		if len(bc.BuildPlatforms) != 1 {
			return nil, nil, nil, fmt.Errorf("expected exactly one build platform, got %d", len(bc.BuildPlatforms))
		}
		buildPlatform = bc.BuildPlatforms[0]

		args := dalec.DuplicateMap(bc.BuildArgs)
		fillPlatformArgs("TARGET", args, targetPlatform)
		fillPlatformArgs("BUILD", args, buildPlatform)
		if err := spec.SubstituteArgs(args); err != nil {
			return nil, nil, nil, err
		}

		ref, img, err := f(ctx, client, spec)
		return ref, img, nil, err
	})
	if err != nil {
		return nil, err
	}

	return rb.Finalize()
}
