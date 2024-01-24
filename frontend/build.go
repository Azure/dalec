package frontend

import (
	"bytes"
	"context"
	"fmt"

	"github.com/Azure/dalec"
	"github.com/containerd/containerd/platforms"
	"github.com/moby/buildkit/exporter/containerimage/image"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests/targets"
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

var PassthroughGetters = map[string]func(ocispecs.Platform) string{
	"TARGETOS": func(p ocispecs.Platform) string {
		return p.OS
	},
	"TARGETARCH": func(p ocispecs.Platform) string {
		return p.Architecture
	},
	"TARGETVARIANT": func(p ocispecs.Platform) string {
		return p.Variant
	},
	"TARGETPLATFORM": func(p ocispecs.Platform) string {
		return platforms.Format(p)
	},
}

func fillPlatformArgs(args map[string]string, platform ocispecs.Platform) map[string]string {
	args = dalec.DuplicateMap(args)
	for v, getter := range PassthroughGetters {
		args[v] = getter(platform)
	}

	return args
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

	rb, err := bc.Build(ctx, func(ctx context.Context, platform *ocispecs.Platform, idx int) (gwclient.Reference, *image.Image, error) {
		var targetPlatform ocispecs.Platform
		if platform != nil {
			targetPlatform = *platform
		} else {
			targetPlatform = platforms.DefaultSpec()
		}

		args := fillPlatformArgs(bc.BuildArgs, targetPlatform)
		if err := spec.SubstituteArgs(args); err != nil {
			return nil, nil, err
		}

		return f(ctx, client, spec)
	})
	if err != nil {
		return nil, err
	}

	return rb.Finalize()
}
