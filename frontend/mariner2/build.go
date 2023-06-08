package mariner2

import (
	"bytes"
	"context"
	"fmt"

	"github.com/azure/dalec/frontend"
	"github.com/moby/buildkit/exporter/containerimage/image"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

func Build(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	bc, err := dockerui.NewClient(client)
	if err != nil {
		return nil, err
	}

	src, err := bc.ReadEntrypoint(ctx, "Dockerfile")
	if err != nil {
		return nil, err
	}

	rb, err := bc.Build(ctx, func(ctx context.Context, platform *ocispecs.Platform, idx int) (gwclient.Reference, *image.Image, error) {
		dt := bytes.TrimSpace(src.Data)
		if bytes.HasPrefix(dt, []byte("# syntax=")) {
			dt = append([]byte("//"), dt[1:]...)
		}

		spec, err := frontend.LoadSpec(dt)
		if err != nil {
			return nil, nil, fmt.Errorf("error loading spec: %w", err)
		}
		if err := client.Warn(ctx, "deadbeef", spec.Name, gwclient.WarnOpts{}); err != nil {
			return nil, nil, err
		}

		st, img, err := Convert(ctx, spec)
		if err != nil {
			return nil, nil, err
		}

		def, err := st.Marshal(ctx)
		if err != nil {
			return nil, nil, err
		}

		res, err := client.Solve(ctx, gwclient.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, nil, err
		}

		ref, err := res.SingleRef()
		if err != nil {
			return nil, nil, err
		}
		return ref, img, nil
	})

	if err != nil {
		return nil, err
	}

	return rb.Finalize()
}
