package windows

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"runtime"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

const (
	defaultBaseImage = "mcr.microsoft.com/windows/nanoserver:1809"
	windowsSystemDir = "/Windows/System32/"
)

var (
	defaultPlatform = ocispecs.Platform{
		OS: outputKey,
		// NOTE: Windows is (currently) only supported on amd64.
		// Making this use runtime.GOARCH so that builds are more explicitly and not surprising.
		// If/when Windows is supported on another platform (ie arm64) this will work as expected.
		// Until then, if someone really wants to build an amd64 image from arm64 they'll need to set the platform explicitly in the build request.
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
		worker, err := distroConfig.Worker(sOpt, pg)
		if err != nil {
			return nil, nil, err
		}

		bin, err := buildBinaries(ctx, spec, worker, client, sOpt, targetKey)
		if err != nil {
			return nil, nil, fmt.Errorf("unable to build binary %w", err)
		}

		baseImgName := getBaseOutputImage(spec, targetKey, defaultBaseImage)
		baseImage := llb.Image(baseImgName, llb.Platform(targetPlatform))

		out := baseImage.
			File(llb.Copy(bin, "/", windowsSystemDir)).
			With(copySymlinks(spec.GetImagePost(targetKey)))

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

		imgRef := dalec.GetBaseOutputImage(spec, targetKey)
		if imgRef == "" {
			imgRef = defaultBaseImage
		}

		_, _, dt, err := client.ResolveImageConfig(ctx, imgRef, sourceresolver.Opt{
			Platform: &targetPlatform,
		})
		if err != nil {
			return nil, nil, errors.Wrap(err, "could not resolve base image config")
		}

		var img dalec.DockerImageSpec
		if err := json.Unmarshal(dt, &img); err != nil {
			return nil, nil, errors.Wrap(err, "error unmarshalling base image config")
		}

		if err := dalec.BuildImageConfig(spec, targetKey, &img); err != nil {
			return nil, nil, errors.Wrap(err, "error creating image config")
		}

		ref, err := res.SingleRef()
		return ref, &img, err
	})
}

func copySymlinks(post *dalec.PostInstall) llb.StateOption {
	return func(s llb.State) llb.State {
		if post == nil {
			return s
		}

		lm := post.Symlinks
		if len(lm) == 0 {
			return s
		}
		keys := dalec.SortMapKeys(lm)
		for _, srcPath := range keys {
			l := lm[srcPath]
			dstPath := l.Path
			s = s.File(llb.Mkdir(path.Dir(dstPath), 0755, llb.WithParents(true)))
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
