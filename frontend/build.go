package frontend

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/Azure/dalec"
	"github.com/containerd/containerd/platforms"
	"github.com/moby/buildkit/exporter/containerimage/image"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests/targets"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

func initGraph(ctx context.Context, client *dockerui.Client, subTarget, dalecTarget string) error {
	src, err := client.ReadEntrypoint(ctx, "Dockerfile")
	if err != nil {
		return fmt.Errorf("could not read spec file: %w", err)
	}

	specs, err := dalec.LoadSpecs(bytes.TrimSpace(src.Data))
	if err != nil {
		return fmt.Errorf("error loading spec: %w", err)
	}

	return dalec.InitGraph(specs, subTarget, dalecTarget)
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

	subTarget, dalecTarget, err := getTargets(bc.Target, bc)
	if err != nil {
		return nil, err
	}

	if err := initGraph(ctx, bc, subTarget, dalecTarget); err != nil {
		return nil, err
	}

	if subTarget == "" && dalec.BuildGraph.OrderedLen("") > 1 {
		return nil, fmt.Errorf("no subtarget specified in multi-spec file")
	}

	// return nil, fmt.Errorf("daled")
	if dalec.BuildGraph.OrderedLen("") == 1 {
		subTarget = dalec.BuildGraph.OrderedSlice("")[0].Name
	}

	for _, spec := range dalec.BuildGraph.OrderedSlice(subTarget) {
		if err := registerSpecHandlers(ctx, spec, client); err != nil {
			return nil, err
		}
	}

	ordered := dalec.BuildGraph.OrderedSlice(subTarget)
	if err != nil {
		return nil, err
	}
	spec := ordered[len(ordered)-1]

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

	f, err := lookupHandler(dalecTarget)
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
		for _, spec := range dalec.BuildGraph.OrderedSlice(subTarget) {
			dupe := dalec.DuplicateMap(args)
			if err := spec.SubstituteArgs(dupe); err != nil {
				return nil, nil, err
			}
		}

		return f(ctx, client, spec)
	})
	if err != nil {
		return nil, err
	}

	return rb.Finalize()

}

func getTargets(tgt string, bc *dockerui.Client) (string, string, error) {
	if tgt == "" {
		dalecTarget := registeredHandlers.Default().Name
		return "", dalecTarget, nil
	}

	if existing := registeredHandlers.Get(tgt); existing != nil {
		dalecTarget := tgt
		return "", dalecTarget, nil
	}

	subTarget, dalecTarget, ok := strings.Cut(bc.Target, "/")
	if !ok {
		return "", "", fmt.Errorf("malformed target: %q", bc.Target)
	}

	return subTarget, dalecTarget, nil
}
