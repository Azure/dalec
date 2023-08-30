package rpmbundle

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/image"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
)

func handleBuildRoot(ctx context.Context, client gwclient.Client, spec *frontend.Spec) (gwclient.Reference, *image.Image, error) {
	cf := client.(reexecFrontend)
	localSt, err := cf.CurrentFrontend()
	if err != nil {
		return nil, nil, fmt.Errorf("could not get current frontend: %w", err)
	}
	caps := client.BuildOpts().LLBCaps
	noMerge := !caps.Contains(pb.CapMergeOp)

	st, err := specToBuildrootLLB(spec, localSt, noMerge)
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

func specToBuildrootLLB(spec *frontend.Spec, localSt *llb.State, noMerge bool) (llb.State, error) {
	out := llb.Scratch().File(llb.Mkdir("SOURCES", 0755), frontend.WithInternalName("Create SOURCES dir"))

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
			exe, err := os.Executable()
			if err != nil {
				return llb.Scratch(), fmt.Errorf("error getting executable path: %w", err)
			}

			// Resolve any symlinks in the executable path so we don't bust the cache on every build.
			exe, err = filepath.EvalSymlinks(exe)
			if err != nil {
				return llb.Scratch(), fmt.Errorf("error resolving symlink for executable path: %w", err)
			}

			localPath := "/tmp/" + k + "/st"
			dstPath := localPath + "Out/" + k + ".tar.gz"
			localSrcWork := localSt.Run(
				llb.Args([]string{exe, "tar", localPath, dstPath}),
				llb.AddMount(localPath, st, llb.Readonly),
				frontend.WithInternalNamef("Create comrpessed tar of source %q", k),
			).State
			if noMerge {
				out = out.File(llb.Copy(localSrcWork, dstPath, "/SOURCES/"), llb.WithCustomNamef("Copy archive of source %q to SOURCES", k))
				continue
			}
			st = llb.Scratch().File(llb.Copy(localSrcWork, dstPath, "/SOURCES/"), llb.WithCustomNamef("Copy archive of source %q to SOURCES", k))
			diffs = append(diffs, llb.Diff(out, st, frontend.WithInternalNamef("Diff source %q from empty SOURCES", k)))
		} else {
			if noMerge {
				out = out.File(llb.Copy(st, "/", "/SOURCES/"), llb.WithCustomNamef("Copy file source for %q to SOURCES", k))
				continue
			}
			st = llb.Scratch().File(llb.Copy(st, "/", "/SOURCES/"), frontend.WithInternalNamef("Copy file source for %q to SOURCES", k))
			diffs = append(diffs, llb.Diff(out, st, frontend.WithInternalNamef("Diff source %q from empty SOURCES", k)))
		}
	}

	if len(diffs) > 0 {
		out = llb.Merge(append([]llb.State{out}, diffs...), llb.WithCustomName("Merge sources into SOURCES dir"))
	}

	buf := bytes.NewBuffer(nil)
	if err := specTmpl.Execute(buf, newSpecWrapper(spec)); err != nil {
		return llb.Scratch(), fmt.Errorf("could not generate rpm spec file: %w", err)
	}

	out = out.File(llb.Mkdir("SPECS", 0755), frontend.WithInternalName("Create SPECS dir"))
	out = out.File(llb.Mkfile("SPECS/"+spec.Name+".spec", 0640, buf.Bytes()), llb.WithCustomName("Generate rpm spec file - SPECS/"+spec.Name+".spec"))

	return out, nil
}
