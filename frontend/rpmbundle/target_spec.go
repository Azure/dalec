package rpmbundle

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"

	"github.com/azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/image"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

func handleSpec(ctx context.Context, client gwclient.Client, spec *frontend.Spec) (gwclient.Reference, *image.Image, error) {
	st, err := specToRpmSpecLLB(spec, llb.Scratch(), "")
	if err != nil {
		return nil, nil, err
	}

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

func specToRpmSpecLLB(spec *frontend.Spec, in llb.State, dir string) (llb.State, error) {
	buf := bytes.NewBuffer(nil)
	if err := specTmpl.Execute(buf, newSpecWrapper(spec)); err != nil {
		return llb.Scratch(), fmt.Errorf("could not generate rpm spec file: %w", err)
	}

	if dir == "" {
		dir = "SPECS/" + spec.Name
	}

	return in.
			File(llb.Mkdir(dir, 0755, llb.WithParents(true))).
			File(llb.Mkfile(filepath.Join(dir, spec.Name)+".spec", 0640, buf.Bytes())),
		nil
}
