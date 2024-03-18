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

func handleContainer(ctx context.Context, client gwclient.Client, spec *dalec.Spec) (gwclient.Reference, *dalec.DockerImageSpec, error) {
	sOpt, err := frontend.SourceOptFromClient(ctx, client)
	if err != nil {
		return nil, nil, err
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

	bin, err := buildBinaries(spec, worker, sOpt)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to build binary %w", err)
	}

	baseImgName := getBaseOutputImage(spec, targetKey, defaultBaseImage)
	baseImage := llb.Image(baseImgName, llb.Platform(targetPlatform))
	out := baseImage.File(llb.Copy(bin, "/", windowsSystemDir))

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

	img, err := dalec.BuildImageConfig(ctx, client, spec, targetKey, defaultBaseImage, dalec.WithPlatform(targetPlatform))
	if err != nil {
		return nil, nil, err
	}

	ref, err := res.SingleRef()

	return ref, img, err
}

func getTargetPlatform(bc *dockerui.Client) (ocispecs.Platform, error) {
	platform := defaultPlatform

	switch len(bc.TargetPlatforms) {
	case 0:
	case 1:
		platform = bc.TargetPlatforms[0]
	default:
		return ocispecs.Platform{}, fmt.Errorf("multiple targets for a windows build, only amd64 is supported")
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
