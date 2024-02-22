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

func loadSpecs(ctx context.Context, client *dockerui.Client) ([]*dalec.Spec, error) {
	src, err := client.ReadEntrypoint(ctx, "Dockerfile")
	if err != nil {
		return nil, fmt.Errorf("could not read spec file: %w", err)
	}

	return dalec.LoadSpecs(bytes.TrimSpace(src.Data))
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

	subtarget, dalecTarget, err := getTargetNames(bc.Target)
	if err != nil {
		return nil, err
	}

	specs, err := loadSpecs(ctx, bc)
	if err != nil {
		return nil, fmt.Errorf("unable to load specs: %w", err)
	}

	if subtarget == "" {
		tgt, err := getDefaultSubtarget(specs, subtarget)
		if err != nil {
			return nil, err
		}

		subtarget = tgt
	}

	if err := dalec.InitGraph(specs, subtarget, dalecTarget); err != nil {
		return nil, fmt.Errorf("failed to build dependency graph: %w", err)
	}

	// By this point, we know the subtarget and only need the deps up through
	// the subtarget, in dependency order.
	ordered := dalec.BuildGraph.Ordered()
	if len(ordered) == 0 {
		return nil, fmt.Errorf("dependency graph failed to resolve")
	}

	for _, spec := range ordered {
		if err := registerSpecHandlers(ctx, &spec, client); err != nil {
			return nil, err
		}
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

		ordered := dalec.BuildGraph.Ordered()
		allArgs := collectBuildArgs(bc.BuildArgs, targetPlatform, buildPlatform, ordered)
		dalec.BuildGraph.SubstituteArgs(allArgs)

		return f(ctx, client, &spec)
	})
	if err != nil {
		return nil, err
	}

	return rb.Finalize()

}

func collectBuildArgs(buildArgs map[string]string, targetPlatform ocispecs.Platform, buildPlatform ocispecs.Platform, specs []dalec.Spec) map[string]map[string]string {
	const tgt = "TARGET"
	const bld = "BUILD"

	args := dalec.DuplicateMap(buildArgs)
	fillPlatformArgs(tgt, args, targetPlatform)
	fillPlatformArgs(bld, args, buildPlatform)
	allArgs := make(map[string]map[string]string)

	for _, spec := range specs {
		name := spec.Name
		dupe := dalec.DuplicateMap(args)
		allArgs[name] = dupe
	}

	return allArgs
}

func getDefaultSubtarget(specs []*dalec.Spec, subTarget string) (string, error) {
	if len(specs) == 0 {
		return "", fmt.Errorf("no spec files provided")
	}

	if subTarget == "" {
		subTarget = specs[len(specs)-1].Name
	}
	return subTarget, nil
}

func getTargetNames(tgt string) (string, string, error) {
	// The user provided no target, use the default and the subtarget will be
	// determined later
	if tgt == "" {
		dalecTarget := registeredHandlers.Default().Name
		return "", dalecTarget, nil
	}

	// The user provided a known target with no subtarget. We will determine
	// the subtarget later
	if existing := registeredHandlers.Get(tgt); existing != nil {
		dalecTarget := tgt
		return "", dalecTarget, nil
	}

	// The user provided a subtarget and target in the form subtarget/target
	subTarget, dalecTarget, ok := strings.Cut(tgt, "/")
	if !ok {
		return "", "", fmt.Errorf("malformed target: %q", tgt)
	}

	return subTarget, dalecTarget, nil
}
