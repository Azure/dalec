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

func loadSpec(ctx context.Context, client *dockerui.Client) (*dalec.Spec, error) {
	g, err := loadSpecs(ctx, client)
	if err != nil {
		return nil, err
	}

	if len(g.Ordered()) == 0 {
		return nil, fmt.Errorf("no spec was provided")
	}

	return g.Last(), nil
}

func loadSpecs(ctx context.Context, client *dockerui.Client) (*dalec.Graph, error) {
	src, err := client.ReadEntrypoint(ctx, "Dockerfile")
	if err != nil {
		return nil, fmt.Errorf("could not read spec file: %w", err)
	}

	specs, err := dalec.LoadSpecs(bytes.TrimSpace(src.Data))
	if err != nil {
		return nil, fmt.Errorf("error loading spec: %w", err)
	}

	return dalec.BuildGraph(specs)
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

	_, target, ok := strings.Cut(bc.Target, "/")
	if !ok {
		return nil, fmt.Errorf("no path separator found: %q", target)
	}

	graph, err := loadSpecs(ctx, bc)
	if err != nil {
		return nil, err
	}

	for _, spec := range graph.Specs {
		if err := registerSpecHandlers(ctx, spec, client); err != nil {
			return nil, err
		}
	}

	spec := graph.Last()

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

	f, err := lookupHandler(target)
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

// func specToState(ctx context.Context, client gwclient.Client, bc *dockerui.Client, spec *dalec.Spec) (llb.State, error) {
// 	res, err := buildOne(ctx, client, bc, spec)
// 	if err != nil {
// 		return llb.Scratch(), err
// 	}
// 	ref, err := res.SingleRef()
// 	if err != nil {
// 		return llb.Scratch(), err
// 	}
// 	st, err := ref.ToState()
// 	if err != nil {
// 		return llb.Scratch(), err
// 	}
// 	return st, nil
// }

// func buildSeveral(ctx context.Context, client gwclient.Client, bc *dockerui.Client, specs []*dalec.Spec) (*gwclient.Result, error) {
// 	for _, spec := range specs {
// 		res, handled, err := bc.HandleSubrequest(ctx, makeRequestHandler(bc.Target))
// 		if err != nil || handled {
// 			return res, err
// 		}

// 		if !handled {
// 			// Handle additional subrequests supported by dalec
// 			requestID := client.BuildOpts().Opts[requestIDKey]
// 			switch requestID {
// 			case dalecSubrequstForwardBuild:
// 			case "":
// 			default:
// 				return nil, fmt.Errorf("unknown request id %q", requestID)
// 			}
// 		}

// 		f, err := lookupHandler(bc.Target)
// 		if err != nil {
// 			return nil, err
// 		}

// 		rb, err := bc.Build(ctx, func(ctx context.Context, platform *ocispecs.Platform, idx int) (gwclient.Reference, *image.Image, error) {
// 			var targetPlatform, buildPlatform ocispecs.Platform
// 			if platform != nil {
// 				targetPlatform = *platform
// 			} else {
// 				targetPlatform = platforms.DefaultSpec()
// 			}

// 			// the dockerui client, given the current implementation, should only ever have
// 			// a single build platform
// 			if len(bc.BuildPlatforms) != 1 {
// 				return nil, nil, fmt.Errorf("expected exactly one build platform, got %d", len(bc.BuildPlatforms))
// 			}
// 			buildPlatform = bc.BuildPlatforms[0]

// 			args := dalec.DuplicateMap(bc.BuildArgs)
// 			fillPlatformArgs("TARGET", args, targetPlatform)
// 			fillPlatformArgs("BUILD", args, buildPlatform)
// 			if err := spec.SubstituteArgs(args); err != nil {
// 				return nil, nil, err
// 			}

// 			return f(ctx, client, spec)
// 		})
// 		if err != nil {
// 			return nil, err
// 		}

// 		return rb.Finalize()

// 	}
// 	return gwclient.NewResult(), nil
// }

// func buildSeveral(ctx context.Context, client gwclient.Client, bc *dockerui.Client, graph *dalec.Graph) (*gwclient.Result, error) {
// 	orderedList := graph.Ordered()
// 	switch len(orderedList) {
// 	case 1:
// 		buildOne(ctx, client, bc, graph)
// 	case 0:
// 		return nil, fmt.Errorf("no dalec build specs")
// 	}

// 	// for _, targets := range orderedList {

// 	// }

// 	// for _, dep := range orderedList {
// 	//     spec := graph.Specs[dep]
// 	// }

// 	panic("unimplemented")
// }
