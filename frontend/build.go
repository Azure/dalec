package frontend

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/azure/dalec"
	"github.com/moby/buildkit/exporter/containerimage/image"
	"github.com/moby/buildkit/frontend/dockerui"
	"github.com/moby/buildkit/frontend/gateway/client"
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

func makeRequestHandler() dockerui.RequestHandler {
	h := dockerui.RequestHandler{}

	h.ListTargets = func(ctx context.Context) (*targets.List, error) {
		var ls targets.List
		for _, t := range registeredTargets.All() {
			ls.Targets = append(ls.Targets, t.Target)
		}
		return &ls, nil
	}

	return h
}

func Build(ctx context.Context, client client.Client) (*client.Result, error) {
	bc, err := dockerui.NewClient(client)
	if err != nil {
		return nil, fmt.Errorf("could not create build client: %w", err)
	}

	res, handled, err := bc.HandleSubrequest(ctx, makeRequestHandler())
	if err != nil || handled {
		return res, err
	}

	tw := registeredTargets.Get(bc.Target)
	if tw == nil {
		tw = registeredTargets.Default()
	}
	if tw == nil {
		var have []string
		for _, t := range registeredTargets.All() {
			have = append(have, t.Name)
		}
		return nil, fmt.Errorf("unknown target %q: available targets: %s", bc.Target, strings.Join(have, ", "))
	}

	rb, err := bc.Build(ctx, func(ctx context.Context, platform *oicspecs.Platform, idx int) (gwclient.Reference, *image.Image, error) {
		spec, err := loadSpec(ctx, bc)
		if err != nil {
			return nil, nil, err
		}
		return tw.Build(ctx, client, spec)
	})
	if err != nil {
		return nil, err
	}

	return rb.Finalize()
}
