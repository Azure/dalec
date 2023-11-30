package rpm

import (
	"context"
	"fmt"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/image"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/identity"
)

// TarImageRef is the image used to create tarballs of sources
// This is purposefully exported so it can be overridden at compile time if needed.
// Currently this image needs /bin/sh and tar in $PATH
var TarImageRef = "busybox:latest"

func HandleSources(ctx context.Context, client gwclient.Client, spec *dalec.Spec) (gwclient.Reference, *image.Image, error) {
	sOpt, err := frontend.SourceOptFromClient(ctx, client)
	if err != nil {
		return nil, nil, err
	}

	sources, err := Dalec2SourcesLLB(spec, sOpt, llb.Image(TarImageRef, llb.WithMetaResolver(sOpt.Resolver)))
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
	return ref, &image.Image{}, err
}

// Dalec2SourcesLLB takes a dalec spec and returns a list of states that contain the sources
func Dalec2SourcesLLB(spec *dalec.Spec, sOpt dalec.SourceOpts, work llb.State) ([]llb.State, error) {
	pgID := identity.NewID()

	// Sort the map keys so that the order is consistent This shouldn't be
	// needed when MergeOp is supported, but when it is not this will improve
	// cache hits for callers of this function.
	sorted := dalec.SortMapKeys(spec.Sources)

	out := make([]llb.State, 0, len(spec.Sources))
	for _, k := range sorted {
		src := spec.Sources[k]
		isDir, err := dalec.SourceIsDir(src)
		if err != nil {
			return nil, err
		}

		pg := llb.ProgressGroup(pgID, "Add spec source: "+k+" "+src.Ref, false)
		st, err := dalec.Source2LLBGetter(spec, src, k)(sOpt, pg)
		if err != nil {
			return nil, err
		}

		if isDir {
			out = append(out, dalec.Tar(work, st, k+".tar.gz", pg))
		} else {
			out = append(out, st)
		}
	}

	return out, nil
}
