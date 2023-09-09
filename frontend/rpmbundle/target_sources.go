package rpmbundle

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/image"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
)

// TarImageRef is the image used to create tarballs of sources
// This is purposefully exported so it can be overridden at compile time if needed.
// Currently this image needs /bin/sh and tar in $PATH
var TarImageRef = "busybox:latest"

func tar(src llb.State, dest string) llb.State {
	// This runs a dummy command to ensure dirs like /proc and /sys are created in the produced state
	// This way we can use llb.Diff to get just the tarball.
	base := llb.Image(TarImageRef).Run(shArgs(":")).State.File(llb.Mkdir(filepath.Dir(dest), 0755, llb.WithParents(true)))

	st := base.Run(
		llb.AddMount("/src", src, llb.Readonly),
		shArgs("tar -C /src -cvzf "+dest+" ."),
	).State

	return llb.Diff(base, st)
}

func handleSources(ctx context.Context, client gwclient.Client, spec *frontend.Spec) (gwclient.Reference, *image.Image, error) {
	caps := client.BuildOpts().LLBCaps
	noMerge := !caps.Contains(pb.CapMergeOp)

	st, err := specToSourcesLLB(spec, noMerge, llb.Scratch(), "SOURCES")
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

func sortMapKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func specToSourcesLLB(spec *frontend.Spec, noMerge bool, in llb.State, target string) (llb.State, error) {
	out := in.File(llb.Mkdir(target, 0755, llb.WithParents(true)))

	diffs := make([]llb.State, 0, len(spec.Sources))

	// Sort the keys so we get a consistent order
	// This is important for caching, especially when noMerge==true
	keys := sortMapKeys(spec.Sources)

	for _, k := range keys {
		src := spec.Sources[k]
		st, err := frontend.Source2LLB(src)
		if err != nil {
			return llb.Scratch(), fmt.Errorf("error converting source %s: %w", k, err)
		}

		isDir, err := frontend.SourceIsDir(src)
		if err != nil {
			return llb.Scratch(), err
		}

		if isDir {
			dstPath := filepath.Join(target, k+".tar.gz")
			tarSt := tar(st, dstPath)
			if noMerge {
				out = out.File(llb.Copy(tarSt, dstPath, dstPath))
				continue
			}
			diffs = append(diffs, llb.Diff(out, tarSt))
		} else {
			if noMerge {
				out = out.File(llb.Copy(st, "/", target+"/"))
				continue
			}
			st = in.File(llb.Copy(st, "/", target+"/"))
			diffs = append(diffs, llb.Diff(out, st))
		}
	}

	if len(diffs) > 0 {
		out = llb.Merge(append([]llb.State{out}, diffs...))
	}
	return out, nil
}
