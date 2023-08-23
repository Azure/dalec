package rpmbundle

import (
	"bytes"
	"context"
	"fmt"
	"os"

	"github.com/azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/image"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

type reexecFrontend interface {
	CurrentFrontend() (*llb.State, error)
}

func Build(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	bc, err := dockerui.NewClient(client)
	if err != nil {
		return nil, fmt.Errorf("could not create build client: %w", err)
	}

	src, err := bc.ReadEntrypoint(ctx, "Dockerfile")
	if err != nil {
		return nil, fmt.Errorf("could not read spec file: %w", err)
	}

	cf := client.(reexecFrontend)
	localSt, err := cf.CurrentFrontend()
	if err != nil {
		return nil, fmt.Errorf("could not get current frontend: %w", err)
	}

	rb, err := bc.Build(ctx, func(ctx context.Context, platform *ocispecs.Platform, idx int) (gwclient.Reference, *image.Image, error) {
		dt := bytes.TrimSpace(src.Data)
		spec, err := frontend.LoadSpec(dt, bc.BuildArgs)
		if err != nil {
			return nil, nil, fmt.Errorf("error loading spec: %w", err)
		}

		st, err := specToLLB(spec, localSt)
		if err != nil {
			return nil, nil, fmt.Errorf("error converting spec to llb: %w", err)
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

func shArgs(args string) llb.RunOption {
	return llb.Args([]string{"/bin/sh", "-c", args})
}

func specToLLB(spec *frontend.Spec, localSt *llb.State) (llb.State, error) {
	out := llb.Scratch().File(llb.Mkdir("SOURCES", 0755))

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
			localPath := "/tmp/" + k + "/st"
			dstPath := localPath + "Out/" + k + ".tar.gz"
			localSrcWork := localSt.Run(
				llb.Args([]string{exe, "tar", localPath, dstPath}),
				llb.AddMount(localPath, st, llb.Readonly),
				llb.WithCustomNamef("Create comrpessed tar of source %q", k),
			).State
			st = llb.Scratch().File(llb.Copy(localSrcWork, dstPath, "/SOURCES/"), llb.WithCustomNamef("Copy tar for source %q to SOURCES", k))
		} else {
			st = llb.Scratch().File(llb.Copy(st, "/", "/SOURCES/"), llb.WithCustomNamef("Copy file source for %q to SOURCES", k))
		}

		diffs = append(diffs, llb.Diff(out, st, llb.WithCustomNamef("Diff source %q from empty SOURCES", k)))
	}

	// TODO: fallback for when `Merge` is not supported
	out = llb.Merge(append([]llb.State{out}, diffs...), llb.WithCustomName("Merge sources into SOURCES dir"))

	buf := bytes.NewBuffer(nil)
	if err := specTmpl.Execute(buf, &specWrapper{
		Spec: spec,
	}); err != nil {
		return llb.Scratch(), fmt.Errorf("could not generate rpm spec file: %w", err)
	}

	out = out.File(llb.Mkfile(spec.Name+".spec", 0640, buf.Bytes()), llb.WithCustomName("Generate rpm spec file"))

	return out, nil
}
