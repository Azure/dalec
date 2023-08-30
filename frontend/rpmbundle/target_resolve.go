package rpmbundle

import (
	"context"
	"fmt"

	"github.com/azure/dalec/frontend"
	"github.com/goccy/go-yaml"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/image"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

func handleResolve(ctx context.Context, client gwclient.Client, spec *frontend.Spec) (gwclient.Reference, *image.Image, error) {
	dt, err := yaml.Marshal(spec)
	if err != nil {
		return nil, nil, fmt.Errorf("error marshalling spec: %w", err)
	}
	st := llb.Scratch().File(llb.Mkfile("spec.yaml", 0640, dt), llb.WithCustomName("Generate resolved spec file - spec.yaml"))
	def, err := st.Marshal(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("error marshalling llb: %w", err)
	}
	res, err := client.Solve(ctx, gwclient.SolveRequest{
		Definition: def.ToPB(),
	})
	if err != nil {
		return nil, nil, err
	}
	ref, err := res.SingleRef()
	return ref, nil, err
}
