package rpm

import (
	"context"
	"fmt"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

func shArgs(cmd string) llb.RunOption {
	return llb.Args([]string{"sh", "-c", cmd})
}

func HandleSources(ctx context.Context, client gwclient.Client, spec *dalec.Spec) (gwclient.Reference, *dalec.DockerImageSpec, error) {
	sOpt, err := frontend.SourceOptFromClient(ctx, client)
	if err != nil {
		return nil, nil, err
	}

	sources, err := Dalec2SourcesLLB(spec, sOpt, dalec.DefaultTarWorker(client))
	if err != nil {
		return nil, nil, err
	}

	// Now we can merge sources into the desired path
	st := dalec.MergeAtPath(llb.Scratch(), sources, "/SOURCES")

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
	return ref, &dalec.DockerImageSpec{}, err
}

func Dalec2SourcesLLB(spec *dalec.Spec, sOpt dalec.SourceOpts, worker llb.State, opts ...llb.ConstraintsOpt) ([]llb.State, error) {
	sources, err := dalec.SourcesWithMod(spec, sOpt, dalec.SourceToTar(worker))
	if err != nil {
		return nil, err
	}

	sorted := dalec.SortMapKeys(sources)
	out := make([]llb.State, 0, len(sorted))
	for _, k := range sorted {
		out = append(out, sources[k])
	}
	return out, nil
}
