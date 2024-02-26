package debug

import (
	"bytes"
	"context"
	"fmt"

	"github.com/Azure/dalec"
	yaml "github.com/goccy/go-yaml"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/image"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

// HandleResolve is a handler that generates a resolved spec file with all the build args and variables expanded.
func HandleResolve(ctx context.Context, client gwclient.Client, graph dalec.Graph) (gwclient.Reference, *image.Image, error) {
	var b bytes.Buffer
	y := yaml.NewEncoder(&b)
	for _, spec := range graph.Ordered() {

		if err := y.Encode(&spec); err != nil {
			return nil, nil, fmt.Errorf("error marshalling spec: %w", err)
		}

	}

	if err := y.Close(); err != nil {
		return nil, nil, fmt.Errorf("error marshalling spec: %w", err)
	}

	st := llb.Scratch().File(llb.Mkfile("spec.yml", 0640, b.Bytes()), llb.WithCustomName("Generate resolved spec file - spec.yml"))
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
	// Do not return a nil image, it may cause a panic
	return ref, &image.Image{}, err

}
