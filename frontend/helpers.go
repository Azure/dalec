package frontend

import (
	"context"
	"fmt"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	"github.com/pkg/errors"
)

type copyOptionFunc func(*llb.CopyInfo)

func (f copyOptionFunc) SetCopyOption(i *llb.CopyInfo) {
	f(i)
}

func WithIncludes(patterns []string) llb.CopyOption {
	return copyOptionFunc(func(i *llb.CopyInfo) {
		i.IncludePatterns = patterns
	})
}

func WithExcludes(patterns []string) llb.CopyOption {
	return copyOptionFunc(func(i *llb.CopyInfo) {
		i.ExcludePatterns = patterns
	})
}

func WithDirContentsOnly() llb.CopyOption {
	return copyOptionFunc(func(i *llb.CopyInfo) {
		i.CopyDirContentsOnly = true
	})
}

type constraintsOptFunc func(*llb.Constraints)

func (f constraintsOptFunc) SetConstraintsOption(c *llb.Constraints) {
	f(c)
}

func (f constraintsOptFunc) SetRunOption(ei *llb.ExecInfo) {
	f(&ei.Constraints)
}

func (f constraintsOptFunc) SetLocalOption(li *llb.LocalInfo) {
	f(&li.Constraints)
}

func (f constraintsOptFunc) SetOCILayoutOption(oi *llb.OCILayoutInfo) {
	f(&oi.Constraints)
}

func (f constraintsOptFunc) SetHTTPOption(hi *llb.HTTPInfo) {
	f(&hi.Constraints)
}

func (f constraintsOptFunc) SetImageOption(ii *llb.ImageInfo) {
	f(&ii.Constraints)
}

func (f constraintsOptFunc) SetGitOption(gi *llb.GitInfo) {
	f(&gi.Constraints)
}

func WithConstraints(ls ...llb.ConstraintsOpt) llb.ConstraintsOpt {
	return constraintsOptFunc(func(c *llb.Constraints) {
		for _, opt := range ls {
			opt.SetConstraintsOption(c)
		}
	})
}

func withConstraints(opts []llb.ConstraintsOpt) llb.ConstraintsOpt {
	return WithConstraints(opts...)
}

// ForwarderFromClient creates a [ForwarderFunc] from a gateway client.
// This is used for forwarding builds to other frontends in [Source2LLBGetter]
func ForwarderFromClient(ctx context.Context, client gwclient.Client) ForwarderFunc {
	return func(st llb.State, spec *BuildSpec) (llb.State, error) {
		if spec == nil {
			spec = &BuildSpec{}
		}

		if spec.File != "" && spec.Inline != "" {
			return llb.Scratch(), fmt.Errorf("cannot specify both file and inline for build spec")
		}

		def, err := st.Marshal(ctx)
		if err != nil {
			return llb.Scratch(), err
		}
		defPb := def.ToPB()

		var dockerfileDt []byte
		if spec.Inline != "" {
			dockerfileDt = []byte(spec.Inline)
		} else {

			// First we need to read the dockerfile to determine what frontend to forward to
			res, err := client.Solve(ctx, gwclient.SolveRequest{
				Definition: defPb,
			})
			if err != nil {
				return llb.Scratch(), errors.Wrap(err, "error getting build context")
			}

			dockerfilePath := dockerui.DefaultDockerfileName
			if spec != nil && spec.File != "" {
				dockerfilePath = spec.File
			}

			ref, err := res.SingleRef()
			if err != nil {
				return llb.Scratch(), err
			}

			dockerfileDt, err = ref.ReadFile(ctx, gwclient.ReadRequest{
				Filename: dockerfilePath,
			})
			if err != nil {
				return llb.Scratch(), errors.Wrap(err, "error reading dockerfile")
			}
		}

		dockerfile := llb.Scratch().File(
			llb.Mkfile("Dockerfile", 0600, dockerfileDt),
		)
		dockerfileDef, err := dockerfile.Marshal(ctx)
		if err != nil {
			return llb.Scratch(), err
		}

		req := gwclient.SolveRequest{
			Frontend: "dockerfile.v0",
			FrontendInputs: map[string]*pb.Definition{
				dockerui.DefaultLocalNameContext: defPb,
				"dockerfile":                     dockerfileDef.ToPB(),
			},
			FrontendOpt: map[string]string{},
		}

		if ref, cmdline, _, ok := parser.DetectSyntax(dockerfileDt); ok {
			req.Frontend = "gateway.v0"
			req.FrontendOpt["source"] = ref
			req.FrontendOpt["cmdline"] = cmdline
		}

		if spec != nil {
			if spec.Target != "" {
				req.FrontendOpt["target"] = spec.Target
			}
			for k, v := range spec.Args {
				req.FrontendOpt["build-arg:"+k] = v
			}
		}

		res, err := client.Solve(ctx, req)
		if err != nil {
			return llb.Scratch(), err
		}
		ref, err := res.SingleRef()
		if err != nil {
			return llb.Scratch(), err
		}
		return ref.ToState()
	}
}
