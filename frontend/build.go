package frontend

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/azure/dalec"
	"github.com/moby/buildkit/exporter/containerimage/image"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests/targets"
	oicspecs "github.com/opencontainers/image-spec/specs-go/v1"
)

func loadSpec(ctx context.Context, client *dockerui.Client) (*dalec.Spec, error) {
	src, err := client.ReadEntrypoint(ctx, "Dockerfile")
	if err != nil {
		return nil, fmt.Errorf("could not read spec file: %w", err)
	}

	spec, err := dalec.LoadSpec(bytes.TrimSpace(src.Data), client.BuildArgs)
	if err != nil {
		return nil, fmt.Errorf("error loading spec: %w", err)
	}
	return spec, nil
}

func makeRequestHandler(spec *dalec.Spec, client gwclient.Client) dockerui.RequestHandler {
	h := dockerui.RequestHandler{AllowOther: true}

	h.ListTargets = func(ctx context.Context) (*targets.List, error) {
		var ls targets.List

		group := client.BuildOpts().Opts[dalecTargetOptKey]
		if group != "" {
			for _, t := range registeredTargets.GetGroup(group) {
				ls.Targets = append(ls.Targets, t.Target)
			}
		} else {
			for _, t := range registeredTargets.All() {
				ls.Targets = append(ls.Targets, t.Target)
			}
		}
		return &ls, nil
	}

	return h
}

// Build is the main entrypoint for the dalec frontend.
func Build(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	bc, err := dockerui.NewClient(client)
	if err != nil {
		return nil, fmt.Errorf("could not create build client: %w", err)
	}

	spec, err := loadSpec(ctx, bc)
	if err != nil {
		return nil, err
	}

	if err := registerSpecTargets(ctx, spec, client); err != nil {
		return nil, err
	}

	res, handled, err := bc.HandleSubrequest(ctx, makeRequestHandler(spec, client))
	if err != nil || handled {
		return res, err
	}

	// Handle additional subrequests supported by dalec
	requestID := client.BuildOpts().Opts[requestIDKey]
	if !handled {
		switch requestID {
		case dalecSubrequstForwardBuild:
		case "":
		default:
			return nil, fmt.Errorf("unknown request id %q", requestID)
		}
	}

	t := registeredTargets.Get(bc.Target)
	if t == nil {
		var have []string
		for _, t := range registeredTargets.All() {
			have = append(have, t.Name)
		}
		return nil, fmt.Errorf("unknown target %q: available targets: %s", bc.Target, strings.Join(have, ", "))
	}

	rb, err := bc.Build(ctx, func(ctx context.Context, platform *oicspecs.Platform, idx int) (gwclient.Reference, *image.Image, error) {
		return t.Build(ctx, client, spec)
	})
	if err != nil {
		return nil, err
	}

	return rb.Finalize()
}
