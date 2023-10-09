package mariner2

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/azure/dalec"
	"github.com/azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/image"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
)

func handleContainer(ctx context.Context, client gwclient.Client, spec *dalec.Spec) (gwclient.Reference, *image.Image, error) {
	caps := client.BuildOpts().LLBCaps
	noMerge := !caps.Contains(pb.CapMergeOp)

	baseImg, err := getBaseBuilderImg(ctx, client)
	if err != nil {
		return nil, nil, err
	}

	sOpt, err := frontend.SourceOptFromClient(ctx, client)
	if err != nil {
		return nil, nil, err
	}
	st, err := specToContainerLLB(spec, targetKey, noMerge, getDigestFromClientFn(ctx, client), baseImg, sOpt)
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

	copyImageConfig(&img, spec.Targets[targetKey].Image)

	ref, err := res.SingleRef()
	return ref, &img, err
}

func specToContainerLLB(spec *dalec.Spec, target string, noMerge bool, getDigest getDigestFunc, baseImg llb.State, sOpt dalec.SourceOpts) (llb.State, error) {
	st, err := specToRpmLLB(spec, noMerge, getDigest, baseImg, sOpt)
	if err != nil {
		return llb.Scratch(), fmt.Errorf("error creating rpm: %w", err)
	}

	installBase := llb.Image(marinerRef, llb.WithMetaResolver(sOpt.Resolver))
	installed := installBase.
		Run(
			shArgs("tdnf install -y /tmp/rpms/$(uname -m)/*.rpm"),
			llb.AddMount("/tmp/rpms", st, llb.SourcePath("/RPMS")),
			marinerTdnfCache,
		).State

	img := spec.Targets[target].Image
	if img == nil || img.Base == "" {
		return installed, nil
	}

	diff := llb.Diff(installBase, installed)
	return llb.Merge([]llb.State{llb.Image(img.Base), diff}), nil
}

func copyImageConfig(dst *image.Image, src *dalec.ImageConfig) {
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
