package frontend

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/Azure/dalec"
	"github.com/containerd/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
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

	f := bytes.NewBuffer(bytes.TrimSpace(src.Data))
	specs, err := dalec.LoadSpecs(f)
	if err != nil {
		return nil, fmt.Errorf("error loading spec: %w", err)
	}
	if len(specs) == 0 {
		return nil, fmt.Errorf("no spec given")
	}
	return specs[0], nil
}

func loadSpecs(ctx context.Context, client *dockerui.Client, target string) ([]*dalec.Spec, error) {
	src, err := client.ReadEntrypoint(ctx, "Dockerfile")
	if err != nil {
		return nil, fmt.Errorf("could not read spec file: %w", err)
	}

	f := bytes.NewBuffer(bytes.TrimSpace(src.Data))
	specs, err := dalec.LoadSpecs(f)
	if err != nil {
		return nil, fmt.Errorf("error loading spec: %w", err)
	}

	return dalec.TopSort(specs, target)
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

	tgt, rest, ok := strings.Cut(bc.Target, "/")
	if !ok {
		return nil, fmt.Errorf("no path separator found")
	}

	_ = rest
	specs, err := loadSpecs(ctx, bc, tgt)
	if err != nil {
		return nil, err
	}

	for _, spec := range specs {
		if err := registerSpecHandlers(ctx, spec, client); err != nil {
			return nil, err
		}
	}

	if len(specs) != 1 {
		// do my thing
		m := make(map[string]llb.State)
		var last string
		for _, spec := range specs[:len(specs)-1] {
			var st llb.State
			if last != "" {
				st = m[spec.Name]
			}
			st, err := specToState(ctx, client, bc, spec, st)
			if err != nil {
				return nil, err
			}
			last = spec.Name
			m[spec.Name] = st
		}

	}
	return build(ctx, client, bc, last, m, nil)

}

func specToState(ctx context.Context, client gwclient.Client, bc *dockerui.Client, spec *dalec.Spec, st *llb.State) (llb.State, error) {
	res, err := build(ctx, client, bc, spec st)
	if err != nil {
		return llb.Scratch(), err
	}
	ref, err := res.SingleRef()
	if err != nil {
		return llb.Scratch(), err
	}
	sst, err := ref.ToState()
	if err != nil {
		return llb.Scratch(), err
	}
	return sst, nil
}

func build(ctx context.Context, client gwclient.Client, bc *dockerui.Client, spec *dalec.Spec, m map[string]llb.State, st *llb.State) (*gwclient.Result, error) {
	res, handled, err := bc.HandleSubrequest(ctx, makeRequestHandler(bc.Target))
	if err != nil || handled {
		return res, err
	}
    pst := llb.Scratch()
    if st != nil {
        pst = *st
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
		var targetPlatform, buildPlatform ocispecs.Platform
		if platform != nil {
			targetPlatform = *platform
		} else {
			targetPlatform = platforms.DefaultSpec()
		}

		// the dockerui client, given the current implementation, should only ever have
		// a single build platform
		if len(bc.BuildPlatforms) != 1 {
			return nil, nil, fmt.Errorf("expected exactly one build platform, got %d", len(bc.BuildPlatforms))
		}
		buildPlatform = bc.BuildPlatforms[0]

		args := dalec.DuplicateMap(bc.BuildArgs)
		fillPlatformArgs("TARGET", args, targetPlatform)
		fillPlatformArgs("BUILD", args, buildPlatform)
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
