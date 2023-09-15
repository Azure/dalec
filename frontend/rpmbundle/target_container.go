package rpmbundle

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/image"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
)

func handleContainer(ctx context.Context, client gwclient.Client, spec *frontend.Spec) (gwclient.Reference, *image.Image, error) {
	caps := client.BuildOpts().LLBCaps
	noMerge := !caps.Contains(pb.CapMergeOp)

	st, err := specToContainerLLB(spec, noMerge, getDigestFromClientFn(ctx, client), client)
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

	_, _, dt, err := client.ResolveImageConfig(ctx, marinerRef, llb.ResolveImageConfigOpt{})
	if err != nil {
		return nil, nil, fmt.Errorf("error resolving image config: %w", err)
	}

	var img image.Image
	if err := json.Unmarshal(dt, &img); err != nil {
		return nil, nil, fmt.Errorf("error unmarshalling image config: %w", err)
	}

	copyImageConfig(&img, spec.Image)

	ref, err := res.SingleRef()
	return ref, &img, err
}

func specToContainerLLB(spec *frontend.Spec, noMerge bool, getDigest getDigestFunc, mr llb.ImageMetaResolver) (llb.State, error) {
	st, err := specToRpmLLB(spec, noMerge, getDigest, mr)
	if err != nil {
		return llb.Scratch(), fmt.Errorf("error creating rpm: %w", err)
	}

	return llb.Image(marinerRef).
		Run(
			shArgs("tdnf install -y /tmp/rpms/$(uname -m)/*.rpm"),
			llb.AddMount("/tmp/rpms", st, llb.SourcePath("/RPMS")),
			marinerTdnfCache,
		).State, nil
}

func copyImageConfig(dst *image.Image, src *frontend.ImageConfig) {
	if src == nil {
		return
	}

	if src.Entrypoint != nil {
		dst.Config.Entrypoint = src.Entrypoint
		// Reset cmd as this may be totally invalid now
		// This is the same behavior as the Dockerfile frontend
		dst.Config.Cmd = nil
	}
	if src.Cmd != nil {
		dst.Config.Cmd = src.Cmd
	}

	if len(src.Env) > 0 {
		// Env is append only
		// If the env var already exists, replace it
		envIdx := make(map[string]int)
		for i, env := range dst.Config.Env {
			envIdx[env] = i
		}

		for _, env := range src.Env {
			if idx, ok := envIdx[env]; ok {
				dst.Config.Env[idx] = env
			} else {
				dst.Config.Env = append(dst.Config.Env, env)
			}
		}
	}

	if src.WorkingDir != "" {
		dst.Config.WorkingDir = src.WorkingDir
	}
	if src.StopSignal != "" {
		dst.Config.StopSignal = src.StopSignal
	}
}
