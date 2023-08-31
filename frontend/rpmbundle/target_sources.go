package rpmbundle

import (
	"context"
	"fmt"

	"github.com/azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/image"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
)

func handleSources(ctx context.Context, client gwclient.Client, spec *frontend.Spec) (gwclient.Reference, *image.Image, error) {
	cf := client.(reexecFrontend)
	localSt, err := cf.CurrentFrontend()
	if err != nil {
		return nil, nil, fmt.Errorf("could not get current frontend: %w", err)
	}
	caps := client.BuildOpts().LLBCaps
	noMerge := !caps.Contains(pb.CapMergeOp)

	st, err := specToSourcesLLB(spec, localSt, noMerge, llb.Scratch(), "SOURCES")
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
	return ref, nil, err
}

func specToSourcesLLB(spec *frontend.Spec, localSt *llb.State, noMerge bool, in llb.State, target string) (llb.State, error) {
	out := in.File(llb.Mkdir(target, 0755, llb.WithParents(true)), frontend.WithInternalName("Create sources target dir "+target))

	diffs := make([]llb.State, 0, len(spec.Sources))
	for k, src := range spec.Sources {
		st, err := frontend.Source2LLB(src)
		if err != nil {
			return llb.Scratch(), fmt.Errorf("error converting source %s: %w", k, err)
		}

		isDir, err := frontend.SourceIsDir(src)
		if err != nil {
			return llb.Scratch(), err
		}

		if isDir {

			localPath := "/tmp/" + k + "/st"
			dstPath := localPath + "Out/" + k + ".tar.gz"
			localSrcWork := localSt.Run(
				frontendCmd("tar", localPath, dstPath),
				llb.AddMount(localPath, st, llb.Readonly),
				frontend.WithInternalNamef("Create comrpessed tar of source %q", k),
			).State
			if noMerge {
				out = out.File(llb.Copy(localSrcWork, dstPath, target+"/"), llb.WithCustomNamef("Copy archive of source %q to SOURCES", k))
				continue
			}
			st = out.File(llb.Copy(localSrcWork, dstPath, target+"/"), llb.WithCustomNamef("Copy archive of source %q to SOURCES", k))
			diffs = append(diffs, llb.Diff(out, st, frontend.WithInternalNamef("Diff source %q from empty SOURCES", k)))
		} else {
			if noMerge {
				out = out.File(llb.Copy(st, "/", target+"/"), llb.WithCustomNamef("Copy file source for %q to SOURCES", k))
				continue
			}
			st = in.File(llb.Copy(st, "/", target+"/"), frontend.WithInternalNamef("Copy file source for %q to SOURCES", k))
			diffs = append(diffs, llb.Diff(out, st, frontend.WithInternalNamef("Diff source %q from empty SOURCES", k)))
		}
	}

	if len(diffs) > 0 {
		out = llb.Merge(append([]llb.State{out}, diffs...), llb.WithCustomName("Merge sources"))
	}
	return out, nil
}
