package windows

import (
	"context"
	"fmt"
	"runtime"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	defaultBaseImage = "mcr.microsoft.com/windows/nanoserver:1809"
	windowsSystemDir = "/Windows/System32/"
)

var (
	defaultPlatform = ocispecs.Platform{
		OS:           outputKey,
		Architecture: runtime.GOARCH,
	}
)

func handleContainer(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		sOpt, err := frontend.SourceOptFromClient(ctx, client)
		if err != nil {
			return nil, nil, err
		}

		if err := validateRuntimeDeps(spec, targetKey); err != nil {
			return nil, nil, fmt.Errorf("error validating windows spec: %w", err)
		}

		bc, err := dockerui.NewClient(client)
		if err != nil {
			return nil, nil, err
		}

		targetPlatform, err := getTargetPlatform(bc)
		if err != nil {
			return nil, nil, err
		}

		pg := dalec.ProgressGroup("Build windows container: " + spec.Name)
		worker := workerImg(sOpt, pg)

		bin, err := buildBinaries(spec, worker, sOpt, targetKey)
		if err != nil {
			return nil, nil, fmt.Errorf("unable to build binary %w", err)
		}

		baseImgName := getBaseOutputImage(spec, targetKey, defaultBaseImage)
		baseImage := llb.Image(baseImgName, llb.Platform(targetPlatform))

		out := baseImage.
			File(llb.Copy(bin, "/", windowsSystemDir)).
			With(copySymlinks(spec.GetSymlinks(targetKey)))

		def, err := out.Marshal(ctx)
		if err != nil {
			return nil, nil, err
		}

		res, err := client.Solve(ctx, gwclient.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, nil, err
		}

		base := frontend.GetBaseOutputImage(spec, targetKey, defaultBaseImage)

		img, err := frontend.BuildImageConfig(ctx, client, spec, targetKey, base, frontend.WithPlatform(targetPlatform))
		if err != nil {
			return nil, nil, err
		}

		ref, err := res.SingleRef()

		return ref, img, err
	})
}

func copySymlinks(lm map[string]dalec.SymlinkTarget) llb.StateOption {
	return func(s llb.State) llb.State {
		if len(lm) == 0 {
			return s
		}
		keys := dalec.SortMapKeys(lm)
		for _, srcPath := range keys {
			l := lm[srcPath]
			dstPath := l.Path
			s = s.File(llb.Copy(s, srcPath, dstPath))
		}

		return s
	}

}

func getTargetPlatform(bc *dockerui.Client) (ocispecs.Platform, error) {
	platform := defaultPlatform

	switch len(bc.TargetPlatforms) {
	case 0:
	case 1:
		platform = bc.TargetPlatforms[0]
	default:
		return ocispecs.Platform{},
			fmt.Errorf("multiple target supplied for build: %v. note: only amd64 is supported for windows outputs", bc.TargetPlatforms)
	}

	return platform, nil
}

func getBaseOutputImage(spec *dalec.Spec, target, defaultBase string) string {
	baseRef := defaultBase
	if spec.Targets[target].Image != nil && spec.Targets[target].Image.Base != "" {
		baseRef = spec.Targets[target].Image.Base
	}
	return baseRef
}
