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
	"github.com/moby/buildkit/frontend/dockerui"
	"github.com/moby/buildkit/frontend/gateway/client"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests/outline"
	"github.com/moby/buildkit/frontend/subrequests/targets"
	"github.com/moby/buildkit/solver/pb"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/exp/maps"
)

type reexecFrontend interface {
	CurrentFrontend() (*llb.State, error)
}

func loadSpec(ctx context.Context, client *dockerui.Client) (*frontend.Spec, error) {
	src, err := client.ReadEntrypoint(ctx, "Dockerfile")
	if err != nil {
		return nil, fmt.Errorf("could not read spec file: %w", err)
	}

	spec, err := frontend.LoadSpec(bytes.TrimSpace(src.Data), client.BuildArgs)
	if err != nil {
		return nil, fmt.Errorf("error loading spec: %w", err)
	}
	return spec, nil
}

func handleSubrequest(ctx context.Context, bc *dockerui.Client) (*client.Result, bool, error) {
	return bc.HandleSubrequest(ctx, dockerui.RequestHandler{
		Outline: func(ctx context.Context) (*outline.Outline, error) {
			return nil, fmt.Errorf("not implemented")
		},
		ListTargets: func(ctx context.Context) (*targets.List, error) {
			spec, err := loadSpec(ctx, bc)
			if err != nil {
				return nil, err
			}
			var tl targets.List

			for _, name := range maps.Keys(spec.Targets) {
				tl.Targets = append(tl.Targets, targets.Target{
					Name:    name,
					Default: true,
				})
			}
			return &tl, nil
		},
	})
}

func Build(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	bc, err := dockerui.NewClient(client)
	if err != nil {
		return nil, fmt.Errorf("could not create build client: %w", err)
	}

	res, handled, err := handleSubrequest(ctx, bc)
	if err != nil || handled {
		return res, err
	}

	cf := client.(reexecFrontend)
	localSt, err := cf.CurrentFrontend()
	if err != nil {
		return nil, fmt.Errorf("could not get current frontend: %w", err)
	}
	caps := client.BuildOpts().LLBCaps
	noMerge := !caps.Contains(pb.CapMergeOp)

	rb, err := bc.Build(ctx, func(ctx context.Context, platform *ocispecs.Platform, idx int) (gwclient.Reference, *image.Image, error) {
		spec, err := loadSpec(ctx, bc)
		if err != nil {
			return nil, nil, err
		}

		if bc.Target != "" {
			_, ok := spec.Targets[bc.Target]
			if !ok {
				return nil, nil, fmt.Errorf("target %q not found", bc.Target)
			}
		}

		var st llb.State
		if bc.Target != "" {
			st, err = specToLLB(spec, localSt, noMerge, bc.Target)
			if err != nil {
				return nil, nil, err
			}
		} else {
			// Build all targets
			base := llb.Scratch()
			diffs := make([]llb.State, 0, len(spec.Targets))
			diffs = append(diffs, base)
			for t := range spec.Targets {
				_st, err := specToLLB(spec, localSt, noMerge, t)
				if err != nil {
					return nil, nil, err
				}
				diffs = append(diffs, llb.Diff(base, _st, llb.WithCustomNamef("Diff target %q", t)))
			}
			st = llb.Merge(diffs)
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
	})
	if err != nil {
		return nil, err
	}
	return rb.Finalize()
}

func specToLLB(spec *frontend.Spec, localSt *llb.State, noMerge bool, target string) (llb.State, error) {
	out := llb.Scratch().File(llb.Mkdir("SOURCES", 0755))

	t := spec.Targets[target]
	diffs := make([]llb.State, 0, len(t.Sources))
	for k := range t.Sources {
		src, ok := spec.Sources[k]
		if !ok {
			return llb.Scratch(), fmt.Errorf("source %q not found", k)
		}

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
				llb.WithCustomNamef("Create comrpessed tar of source %q", k),
			).State
			if noMerge {
				out = out.File(llb.Copy(localSrcWork, dstPath, "/SOURCES/"), llb.WithCustomNamef("Copy tar for source %q to SOURCES", k))
				continue
			}
			st = llb.Scratch().File(llb.Copy(localSrcWork, dstPath, "/SOURCES/"), llb.WithCustomNamef("Copy tar for source %q to SOURCES", k))
			diffs = append(diffs, llb.Diff(out, st, llb.WithCustomNamef("Diff source %q from empty SOURCES", k)))
		} else {
			if noMerge {
				out = out.File(llb.Copy(st, "/", "/SOURCES/"), llb.WithCustomNamef("Copy file source for %q to SOURCES", k))
				continue
			}
			st = llb.Scratch().File(llb.Copy(st, "/", "/SOURCES/"), llb.WithCustomNamef("Copy file source for %q to SOURCES", k))
			diffs = append(diffs, llb.Diff(out, st, llb.WithCustomNamef("Diff source %q from empty SOURCES", k)))
		}
	}

	if len(diffs) > 0 {
		out = llb.Merge(append([]llb.State{out}, diffs...), llb.WithCustomName("Merge sources into SOURCES dir"))
	}

	buf := bytes.NewBuffer(nil)
	if err := specTmpl.Execute(buf, newSpecWrapper(spec, target)); err != nil {
		return llb.Scratch(), fmt.Errorf("could not generate rpm spec file: %w", err)
	}

	out = out.File(llb.Mkdir("SPECS", 0755))
	out = out.File(llb.Mkfile("SPECS/"+spec.Name+".spec", 0640, buf.Bytes()), llb.WithCustomName("Generate rpm spec file - SPECS/"+spec.Name+".spec"))

	return out, nil
}
