package debug

import (
	"context"
	"net"
	"strings"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

const keyGomodWorker = "gomod-worker"

// Gomods outputs all the gomodule dependencies for the spec
func Gomods(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
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
		worker, ok := inputs[keyGomodWorker]
		if !ok {
			worker = llb.Image("alpine:latest", llb.WithMetaResolver(client)).
				Run(llb.Shlex("apk add --no-cache go git ca-certificates patch openssh")).Root()
		}
		worker = worker.With(addedHosts(client))

		st, err := spec.GomodDeps(sOpt, worker, dalec.Platform(platform))
		if err != nil {
			return nil, nil, err
		}

		def, err := st.Marshal(ctx)
		if err != nil {
			return nil, nil, err
		}

		// fopts := getAddedHosts(client)
		res, err := client.Solve(ctx, gwclient.SolveRequest{
			Definition: def.ToPB(),
			// FrontendOpt: fopts,
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

func getAddedHosts(client gwclient.Client) map[string]string {
	bopts := client.BuildOpts().Opts
	var fopts map[string]string
	if v, ok := bopts["add-hosts"]; ok {
		fopts = make(map[string]string)
		fopts["add-hosts"] = v
	}
	return fopts
}

func addedHosts(client gwclient.Client) llb.StateOption {
	return func(s llb.State) llb.State {
		ret := s
		bopts := client.BuildOpts().Opts
		if v, ok := bopts["add-hosts"]; ok {
			pairs := strings.Split(v, ",")
			for _, pair := range pairs {
				key, val, _ := strings.Cut(pair, "=")
				ret = ret.AddExtraHost(key, net.ParseIP(val))
			}
		}

		return ret
	}
}
