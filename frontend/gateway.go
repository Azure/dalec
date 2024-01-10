package frontend

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"

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

func getDockerfile(ctx context.Context, client gwclient.Client, build *dalec.SourceBuild, defPb *pb.Definition) ([]byte, error) {
	dockerfilePath := dockerui.DefaultDockerfileName

	switch {
	case build.Inline != "":
		return []byte(build.Inline), nil
	case build.DockerFile != "":
		dockerfilePath = build.DockerFile
	}

	// First we need to read the dockerfile to determine what frontend to forward to
	res, err := client.Solve(ctx, gwclient.SolveRequest{
		Definition: defPb,
	})
	if err != nil {
		return nil, errors.Wrap(err, "error getting build context")
	}

	ref, err := res.SingleRef()
	if err != nil {
		return nil, err
	}

	dt, err := ref.ReadFile(ctx, gwclient.ReadRequest{
		Filename: dockerfilePath,
	})
	if err != nil {
		return nil, errors.Wrap(err, "error reading dockerfile")
	}
	return dt, nil
}

// ForwarderFromClient creates a [dalec.ForwarderFunc] from a gateway client.
// This is used for forwarding builds to other frontends in [dalec.Source2LLBGetter]
func ForwarderFromClient(ctx context.Context, client gwclient.Client) dalec.ForwarderFunc {
	return func(st llb.State, spec *dalec.SourceBuild) (llb.State, error) {
		if spec == nil {
			spec = &dalec.SourceBuild{}
		}

		def, err := st.Marshal(ctx)
		if err != nil {
			return llb.Scratch(), err
		}
		defPb := def.ToPB()

		dockerfileDt, err := getDockerfile(ctx, client, spec, defPb)
		if err != nil {
			return llb.Scratch(), err
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

		if err := copyForForward(ctx, client, &req); err != nil {
			return llb.Scratch(), err
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

func makeTargetForwarder(specT dalec.Target, bkt bktargets.Target) BuildFunc {
	return func(ctx context.Context, client gwclient.Client, spec *dalec.Spec) (_ gwclient.Reference, _ *image.Image, retErr error) {
		defer func() {
			if retErr != nil {
				retErr = errors.Wrapf(retErr, "error forwarding build to frontend %q for target %s", specT.Frontend.Image, bkt.Name)
			}
		}()

		dt, err := yaml.Marshal(spec)
		if err != nil {
			return nil, nil, errors.Wrap(err, "error marshalling spec to yaml")
		}

		def, err := llb.Scratch().File(llb.Mkfile("Dockerfile", 0600, dt)).Marshal(ctx)
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

		if err := copyForForward(ctx, client, &req); err != nil {
			return nil, nil, err
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

var (
	supportsDiffMergeOnce sync.Once
	supportsDiffMerge     atomic.Bool
)

// SupportsDiffMerge checks if the given client supports the diff and merge operations.
func SupportsDiffMerge(client gwclient.Client) bool {
	supportsDiffMergeOnce.Do(func() {
		if client.BuildOpts().Opts["build-arg:DALEC_DISABLE_DIFF_MERGE"] == "1" {
			supportsDiffMerge.Store(false)
			return
		}
		supportsDiffMerge.Store(checkDiffMerge(client))
	})
	return supportsDiffMerge.Load()
}

func checkDiffMerge(client gwclient.Client) bool {
	caps := client.BuildOpts().LLBCaps
	if caps.Supports(pb.CapMergeOp) != nil {
		return false
	}

	if caps.Supports(pb.CapDiffOp) != nil {
		return false
	}
	return true
}

// copyForForward copies all the inputs and build opts from the initial request in order to forward to another frontend.
func copyForForward(ctx context.Context, client gwclient.Client, req *gwclient.SolveRequest) error {
	// Inputs are any additional build contexts or really any llb that the client sent along.
	inputs, err := client.Inputs(ctx)
	if err != nil {
		return err
	}

	if req.FrontendInputs == nil {
		req.FrontendInputs = make(map[string]*pb.Definition, len(inputs))
	}

	for k, v := range inputs {
		if _, ok := req.FrontendInputs[k]; ok {
			// Do not overwrite existing inputs
			continue
		}

		def, err := v.Marshal(ctx)
		if err != nil {
			return errors.Wrap(err, "error marshaling frontend input")
		}
		req.FrontendInputs[k] = def.ToPB()
	}

	opts := client.BuildOpts().Opts
	if req.FrontendOpt == nil {
		req.FrontendOpt = make(map[string]string, len(opts))
	}

	for k, v := range opts {

		if k == "filename" || k == "dockerfilekey" || k == "target" {
			// These are some well-known keys that the dockerfile frontend uses
			// which we'll be overriding with our own values (as needed) in the
			// caller.
			// Basically there should not be a need, nor is it desirable, to forward these along.
			continue
		}

		if _, ok := req.FrontendOpt[k]; ok {
			// Do not overwrite existing opts
			continue
		}
		req.FrontendOpt[k] = v
	}

	return nil
}
