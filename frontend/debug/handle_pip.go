package debug

import (
	"context"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

const keyPipWorker = "context:pip-worker"

// Pip outputs all the pip dependencies for the spec
func Pip(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		sOpt, err := frontend.SourceOptFromClient(ctx, client, platform)
		if err != nil {
			return nil, nil, err
		}

		inputs, err := client.Inputs(ctx)
		if err != nil {
			return nil, nil, err
		}

		// Allow the client to override the worker image
		// This is useful for keeping pre-built worker image, especially for CI.
		worker, ok := inputs[keyPipWorker]
		if !ok {
			worker = llb.Image("python:latest", llb.WithMetaResolver(client)).
				Run(dalec.ShArgs("DEBIAN_FRONTEND=noninteractive apt-get update && apt-get install -y build-essential")).
				Run(dalec.ShArgs("rm -rf /var/lib/apt/lists/*")).
				Run(llb.Shlex("python3 --version")).
				Run(dalec.ShArgs("python3 -m venv /opt/venv && /opt/venv/bin/pip install --upgrade pip")).
				Run(llb.Shlex("python3 -m pip --version")).Root()
		}

		pipSources, err := spec.PipDeps(sOpt, worker, dalec.Platform(platform))
		if err != nil {
			return nil, nil, err
		}

		var st llb.State
		if len(pipSources) == 0 {
			st = llb.Scratch()
		} else {
			// Merge all pip sources into a single state for debugging
			states := make([]llb.State, 0, len(pipSources))
			for k, v := range pipSources {
				subSt := llb.Scratch().File(llb.Copy(v, "/", k))
				states = append(states, subSt)
			}
			st = dalec.MergeAtPath(llb.Scratch(), states, "/")
		}

		def, err := st.Marshal(ctx)
		if err != nil {
			return nil, nil, err
		}

		res, err := client.Solve(ctx, gwclient.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, nil, err
		}

		ref, err := res.SingleRef()
		if err != nil {
			return nil, nil, err
		}
		return ref, &dalec.DockerImageSpec{}, nil
	})
}
