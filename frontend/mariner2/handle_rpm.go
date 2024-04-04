package mariner2

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/frontend/rpm"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/solver/pb"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	marinerRef = "mcr.microsoft.com/cbl-mariner/base/core:2.0"

	initialState = "initialstate"

	tdnfCacheDir  = "/var/cache/tdnf"
	tdnfCacheName = "mariner2-tdnf-cache"
)

func defaultTdnfCacheMount() llb.RunOption {
	return tdnfCacheMountWithPrefix("")
}

func tdnfCacheMountWithPrefix(prefix string) llb.RunOption {
	// note: We are using CacheMountLocked here because without it, while there are concurrent builds happening, tdnf exits with a lock error.
	// dnf is supposed to wait for locks, but it seems like that is not happening with tdnf.
	return llb.AddMount(filepath.Join(prefix, tdnfCacheDir), llb.Scratch(), llb.AsPersistentCacheDir(tdnfCacheName, llb.CacheMountLocked))
}

func hasSigner(t *dalec.Target) bool {
	return t != nil && t.PackageConfig != nil && t.PackageConfig.Signer != nil && t.PackageConfig.Signer.Image != nil
}

func compound(k, v string) string {
	return fmt.Sprintf("%s:%s", k, v)
}

func forwardToSigner(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, cfg *dalec.Signer, s llb.State) (llb.State, error) {
	const (
		sourceKey               = "source"
		contextKey              = "context"
		targetKey               = "target"
		inputKey                = "input"
		resolveModeKey          = "image.resolvemode"
		containerImageConfigKey = "containerimage.config"
		inputMetadataKey        = "input-metadata"

		gatewayFrontend = "gateway.v0"
	)

	opts := client.BuildOpts().Opts
	signer := llb.Image(cfg.Image.Ref)
	id := identity.NewID()

	req := gwclient.SolveRequest{
		Frontend:       gatewayFrontend,
		FrontendOpt:    make(map[string]string),
		FrontendInputs: make(map[string]*pb.Definition),
	}

	_, _, b, err := client.ResolveImageConfig(ctx, cfg.Image.Ref, sourceresolver.Opt{
		Platform: platform,
		ImageOpt: &sourceresolver.ResolveImageOpt{
			ResolveMode: opts[resolveModeKey],
		},
	})

	withConfig, err := signer.WithImageConfig(b)
	if err != nil {
		return llb.Scratch(), err
	}

	signerDef, err := withConfig.Marshal(ctx)
	if err != nil {
		return llb.Scratch(), err
	}
	signerPB := signerDef.ToPB()

	req.FrontendOpt[compound(contextKey, id)] = compound(inputKey, id)
	req.FrontendInputs[id] = signerPB
	req.FrontendOpt[sourceKey] = id
	req.FrontendOpt[compound(contextKey, initialState)] = compound(inputKey, initialState)
	req.FrontendOpt[targetKey] = "check"
	req.FrontendInputs[contextKey] = signerPB

	meta := map[string][]byte{
		containerImageConfigKey: b,
	}
	metaDt, err := json.Marshal(meta)
	if err != nil {
		return llb.Scratch(), fmt.Errorf("error marshaling local frontend metadata: %w", err)
	}
	req.FrontendOpt[compound(inputMetadataKey, id)] = string(metaDt)

	stateDef, err := s.Marshal(ctx)
	if err != nil {
		return llb.Scratch(), err
	}

	req.FrontendInputs[initialState] = stateDef.ToPB()

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

func handleRPM(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		if err := rpm.ValidateSpec(spec); err != nil {
			return nil, nil, fmt.Errorf("rpm: invalid spec: %w", err)
		}

		pg := dalec.ProgressGroup("Building mariner2 rpm: " + spec.Name)
		sOpt, err := frontend.SourceOptFromClient(ctx, client)
		if err != nil {
			return nil, nil, err
		}

		st, err := specToRpmLLB(spec, sOpt, targetKey, pg)
		if err != nil {
			return nil, nil, err
		}

		t := spec.Targets[targetKey]
		if hasSigner(&t) {
			signed, err := forwardToSigner(ctx, client, platform, t.PackageConfig.Signer, st)
			if err != nil {
				return nil, nil, err
			}

			st = signed
		}

		def, err := st.Marshal(ctx, pg)
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
		if err != nil {
			return nil, nil, err
		}

		return ref, nil, nil
	})
}

func shArgs(cmd string) llb.RunOption {
	return llb.Args([]string{"sh", "-c", cmd})
}

func getWorkerImage(resolver llb.ImageMetaResolver, opts ...llb.ConstraintsOpt) llb.State {
	opts = append(opts, dalec.ProgressGroup("Prepare worker image"))
	return llb.Image(marinerRef, llb.WithMetaResolver(resolver), dalec.WithConstraints(opts...)).
		Run(
			shArgs("tdnf install -y rpm-build mariner-rpm-macros build-essential ca-certificates"),
			defaultTdnfCacheMount(),
			dalec.WithConstraints(opts...),
		).
		Root()
}

func installBuildDeps(spec *dalec.Spec, targetKey string, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		deps := spec.GetBuildDeps(targetKey)
		if len(deps) == 0 {
			return in
		}

		opts = append(opts, dalec.ProgressGroup("Install build deps"))
		return in.
			Run(
				shArgs(fmt.Sprintf("tdnf install --releasever=2.0 -y %s", strings.Join(deps, " "))),
				defaultTdnfCacheMount(),
				dalec.WithConstraints(opts...),
			).
			Root()
	}
}

func specToRpmLLB(spec *dalec.Spec, sOpt dalec.SourceOpts, targetKey string, opts ...llb.ConstraintsOpt) (llb.State, error) {
	base, err := getSpecWorker(sOpt.Resolver, spec, targetKey, opts...)
	if err != nil {
		return llb.Scratch(), err
	}

	br, err := rpm.SpecToBuildrootLLB(base, spec, sOpt, targetKey, opts...)
	if err != nil {
		return llb.Scratch(), err
	}
	specPath := filepath.Join("SPECS", spec.Name, spec.Name+".spec")

	return rpm.Build(br, base.With(installBuildDeps(spec, targetKey, opts...)), specPath, opts...), nil
}
