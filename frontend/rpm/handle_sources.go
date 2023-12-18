package rpm

import (
	"context"
	"fmt"
	"path/filepath"

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

func shArgs(cmd string) llb.RunOption {
	return llb.Args([]string{"sh", "-c", cmd})
}

func tar(src llb.State, dest string, opts ...llb.ConstraintsOpt) llb.State {
	tarImg := llb.Image(TarImageRef)

	// Put the output tar in a consistent location regardless of `dest`
	// This way if `dest` changes we don't have to rebuild the tarball, which can be expensive.
	outBase := "/tmp/out"
	out := filepath.Join(outBase, filepath.Dir(dest))
	worker := tarImg.Run(
		llb.AddMount("/src", src, llb.Readonly),
		shArgs("tar -C /src -cvzf /tmp/st ."),
		dalec.WithConstraints(opts...),
	).
		Run(
			shArgs("mkdir -p "+out+" && mv /tmp/st "+filepath.Join(out, filepath.Base(dest))),
			dalec.WithConstraints(opts...),
		)

	return worker.AddMount(outBase, llb.Scratch())
}

func HandleSources(ctx context.Context, client gwclient.Client, spec *dalec.Spec) (gwclient.Reference, *image.Image, error) {
	sOpt, err := frontend.SourceOptFromClient(ctx, client)
	if err != nil {
		return nil, nil, err
	}

	sources, err := Dalec2SourcesLLB(spec, sOpt)
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

func Dalec2SourcesLLB(spec *dalec.Spec, sOpt dalec.SourceOpts) ([]llb.State, error) {
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

		s := ""
		switch {
		case src.DockerImage != nil:
			s = src.DockerImage.Ref
		case src.Git != nil:
			s = src.Git.URL
		case src.HTTPS != nil:
			s = src.HTTPS.URL
		case src.Context != nil:
			s = src.Context.Name
		case src.Build != nil:
			switch {
			case src.Build.Context != nil:
				s = src.Build.Context.Name
			case src.Build.Local != nil:
				s = src.Build.Local.Path
			case src.Build.Source != nil:
				s = src.Build.Source.Name
			}
		default:
		}

		pg := llb.ProgressGroup(pgID, "Add spec source: "+k+" "+s, false)
		st, err := dalec.Source2LLBGetter(spec, src, k)(sOpt, pg)
		if err != nil {
			return nil, err
		}

		if isDir {
			out = append(out, tar(st, k+".tar.gz", pg))
		} else {
			out = append(out, st)
		}
	}

	return out, nil
}
