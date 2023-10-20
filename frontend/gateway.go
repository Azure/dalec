package frontend

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Azure/dalec"
	"github.com/goccy/go-yaml"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	"github.com/moby/buildkit/exporter/containerimage/image"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	bktargets "github.com/moby/buildkit/frontend/subrequests/targets"
	"github.com/moby/buildkit/solver/pb"
	"github.com/pkg/errors"
)

const (
	requestIDKey               = "requestid"
	dalecSubrequstForwardBuild = "dalec.forward.build"
)

// ForwarderFromClient creates a [dalec.ForwarderFunc] from a gateway client.
// This is used for forwarding builds to other frontends in [dalec.Source2LLBGetter].
func ForwarderFromClient(ctx context.Context, client gwclient.Client) dalec.ForwarderFunc {
	return func(st llb.State, spec *dalec.BuildSpec) (llb.State, error) {
		if spec == nil {
			spec = &dalec.BuildSpec{}
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
			llb.Mkfile("Dockerfile", 0o600, dockerfileDt),
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

func GetBuildArg(client gwclient.Client, k string) (string, bool) {
	opts := client.BuildOpts().Opts
	if opts != nil {
		if v, ok := opts["build-arg:"+k]; ok {
			return v, true
		}
	}
	return "", false
}

func makeTargetForwarder(specT dalec.Target, bkt *bktargets.Target) BuildFunc {
	return func(ctx context.Context, client gwclient.Client, spec *dalec.Spec) (_ gwclient.Reference, _ *image.Image, retErr error) {
		defer func() {
			if retErr != nil {
				retErr = errors.Wrapf(retErr, "error forwarding build to frontend %q for target %s", specT.Frontend.Image, bkt.Name)
			}
		}()

		dt, err := yaml.Marshal(spec)
		if err != nil {
			return nil, nil, errors.Wrap(err, "error marshaling spec to yaml")
		}
		def, err := llb.Scratch().File(llb.Mkfile("Dockerfile", 0o600, dt)).Marshal(ctx)
		if err != nil {
			return nil, nil, errors.Wrap(err, "error marshaling dockerfile to LLB")
		}

		req := gwclient.SolveRequest{
			Frontend: "gateway.v0",
			FrontendInputs: map[string]*pb.Definition{
				"dockerfile": def.ToPB(),
			},
			FrontendOpt: map[string]string{
				"source":     specT.Frontend.Image,
				"cmdline":    specT.Frontend.CmdLine,
				"target":     bkt.Name,
				requestIDKey: dalecSubrequstForwardBuild,
			},
		}

		for k, v := range client.BuildOpts().Opts {
			if strings.HasPrefix(k, "build-arg:") {
				req.FrontendOpt[k] = v
			}
		}
		res, err := client.Solve(ctx, req)
		if err != nil {
			return nil, nil, err
		}

		ref, err := res.SingleRef()
		if err != nil {
			return nil, nil, err
		}
		configDt := res.Metadata[exptypes.ExporterImageConfigKey]
		var cfg image.Image
		if err := json.Unmarshal(configDt, &cfg); err != nil {
			return nil, nil, err
		}

		return ref, &cfg, nil
	}
}

func SourceOptFromClient(ctx context.Context, c gwclient.Client) (dalec.SourceOpts, error) {
	dc, err := dockerui.NewClient(c)
	if err != nil {
		return dalec.SourceOpts{}, err
	}
	return dalec.SourceOpts{
		Resolver: c,
		Forward:  ForwarderFromClient(ctx, c),
		GetContext: func(ref string, opts ...llb.LocalOption) (*llb.State, error) {
			if ref == dockerui.DefaultLocalNameContext {
				return dc.MainContext(ctx, opts...)
			}
			st, _, err := dc.NamedContext(ctx, ref, dockerui.ContextOpt{
				ResolveMode: dc.ImageResolveMode.String(),
			})
			if err != nil {
				return nil, err
			}
			return st, nil
		},
	}, nil
}
