package rpm

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/azure/dalec"
	"github.com/azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/image"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/solver/pb"
)

// TarImageRef is the image used to create tarballs of sources
// This is purposefully exported so it can be overridden at compile time if needed.
// Currently this image needs /bin/sh and tar in $PATH
var TarImageRef = "busybox:latest"

func shArgs(cmd string) llb.RunOption {
	return llb.Args([]string{"sh", "-c", cmd})
}

func tar(src llb.State, dest string, opts ...llb.ConstraintsOpt) llb.State {
	// This runs a dummy command to ensure dirs like /proc and /sys are created in the produced state
	// This way we can use llb.Diff to get just the tarball.

	tarImg := llb.Image(TarImageRef).Run(shArgs(":"), dalec.WithConstraints(opts...)).State

	// Put the output tar in a consistent location regardless of `dest`
	// This way if `dest` changes we don't have to rebuild the tarball, which can be expensive.
	base := filepath.Base(dest)
	st := tarImg.Run(
		llb.AddMount("/src", src, llb.Readonly),
		shArgs("tar -C /src -cvzf "+base+" ."),
		dalec.WithConstraints(opts...),
	).State

	if base == dest {
		return llb.Diff(tarImg, st)
	}

	return llb.Scratch().File(llb.Copy(st, base, dest, dalec.WithCreateDestPath()))
}

// mergeOrCopy merges(or copies if noMerge=true) the given states into the given destination path in the given input state.
func mergeOrCopy(input llb.State, states []llb.State, dest string, noMerge bool) llb.State {
	output := input

	if noMerge {
		for _, src := range states {
			if noMerge {
				output = output.File(llb.Copy(src, "/", "/SOURCES/", dalec.WithCreateDestPath()))
			}
		}
		return output
	}

	diffs := make([]llb.State, 0, len(states))
	for _, src := range states {
		st := src
		if dest != "" && dest != "/" {
			st = llb.Scratch().
				File(llb.Copy(src, "/", dest, dalec.WithCreateDestPath()))
		}
		diffs = append(diffs, llb.Diff(input, st))
	}
	return llb.Merge(diffs)
}

func HandleSources(ctx context.Context, client gwclient.Client, spec *dalec.Spec) (gwclient.Reference, *image.Image, error) {
	caps := client.BuildOpts().LLBCaps

	sources, err := Dalec2SourcesLLB(spec, client, frontend.ForwarderFromClient(ctx, client))
	if err != nil {
		return nil, nil, err
	}

	noMerge := !caps.Contains(pb.CapMergeOp)
	// Now we can merge sources into the desired path
	st := mergeOrCopy(llb.Scratch(), sources, "/SOURCES", noMerge)

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

func Dalec2SourcesLLB(spec *dalec.Spec, resolver llb.ImageMetaResolver, forward dalec.ForwarderFunc) ([]llb.State, error) {
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
		st, err := dalec.Source2LLBGetter(spec, src, resolver)(forward, pg)
		if err != nil {
			return nil, err
		}

		if isDir {
			// use /tmp/st as the output tar name so that caching doesn't break based on file path.
			tarSt := tar(st, "/tmp/st", pg)
			out = append(out, llb.Scratch().File(llb.Copy(tarSt, "/tmp/st", k+".tar.gz"), pg))
		} else {
			out = append(out, st)
		}
	}

	return out, nil
}
