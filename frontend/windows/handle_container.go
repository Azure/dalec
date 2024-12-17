package windows

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"runtime"
	"sync"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
)

const (
	defaultBaseImage = "mcr.microsoft.com/windows/nanoserver:1809"
	windowsSystemDir = "/Windows/System32/"

	argBasesPathKey    = "DALEC_WINDOWSCROSS_BASES_PATH"
	argBasesContextKey = "DALEC_WINDOWSCROSS_BASES_CONTEXT"
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

// ImageBases is the structure used by clients that want to specify multiple
// base images for the container build target via the `DALEC_WINDOWSCROSS_BASES_PATH`
// and `DALEC_WINDOWSCROSS_BASES_CONTEXT` build-args.
type ImageBases struct {
	Refs []string `json:"refs,omitempty" yaml:"refs,omitempty"`
}

func (ib *ImageBases) getRefs() []string {
	if ib == nil {
		return nil
	}
	return ib.Refs
}

func (ib *ImageBases) len() int {
	if ib == nil {
		return 0
	}
	return len(ib.Refs)
}

func getImageBases(ctx context.Context, client gwclient.Client, sOpt dalec.SourceOpts) (*ImageBases, error) {

	bOpts := client.BuildOpts().Opts
	p := bOpts["build-arg:"+argBasesPathKey]
	if p == "" {
		return nil, nil
	}

	src := dalec.Source{
		Context: &dalec.SourceContext{Name: "context"},
		Path:    p,
	}

	if name := bOpts["build-arg:"+argBasesContextKey]; name != "" {
		src.Context.Name = name
	}

	pg := dalec.ProgressGroup("Determine base images")
	st, err := src.AsMount("src", sOpt, pg)
	if err != nil {
		return nil, err
	}

	def, err := st.Marshal(ctx, pg)
	if err != nil {
		return nil, errors.Wrapf(err, "marshalling state to solve for \"%s=%s\" and \"%s=%s\"", argBasesPathKey, p, argBasesContextKey, src.Context.Name)
	}

	res, err := client.Solve(ctx, gwclient.SolveRequest{Definition: def.ToPB()})
	if err != nil {
		return nil, errors.Wrapf(err, "solving state for \"%s=%s\" and \"%s=%s\"", argBasesPathKey, p, argBasesContextKey, src.Context.Name)
	}

	ref, err := res.SingleRef()
	if err != nil {
		return nil, err
	}

	dt, err := ref.ReadFile(ctx, gwclient.ReadRequest{Filename: p})
	if err != nil {
		return nil, err
	}

	var bases ImageBases

	if err := json.Unmarshal(dt, &bases); err != nil {
		return nil, err
	}
	return &bases, nil
}

func handleContainer(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	dc, err := dockerui.NewClient(client)
	if err != nil {
		return nil, err
	}

	if len(dc.TargetPlatforms) > 1 {
		return nil, fmt.Errorf("multi-platform output is not supported")
	}

	sOpt := frontend.SourceOptFromUIClient(ctx, client, dc)

	bases, err := getImageBases(ctx, client, sOpt)
	if err != nil {
		return nil, err
	}

	refs := bases.getRefs()
	if len(refs) == 0 {
		refs = append(refs, defaultBaseImage)
	}

	eg, grpCtx := errgroup.WithContext(ctx)
	cfgs := make([][]byte, len(refs))
	targets := make([]ocispecs.Platform, len(cfgs))

	basePlatform := defaultPlatform
	if len(dc.TargetPlatforms) > 0 {
		basePlatform = dc.TargetPlatforms[0]
	}

	for idx, ref := range refs {
		idx := idx
		ref := ref
		eg.Go(func() error {
			_, _, dt, err := client.ResolveImageConfig(grpCtx, ref, sourceresolver.Opt{
				Platform: &basePlatform,
				ImageOpt: &sourceresolver.ResolveImageOpt{
					ResolveMode: dc.ImageResolveMode.String(),
				},
			})

			if err != nil {
				return err
			}

			var cfg dalec.DockerImageSpec
			if err := json.Unmarshal(dt, &cfg); err != nil {
				return errors.Wrapf(err, "image config for %s", ref)
			}

			cfgs[idx] = dt
			targets[idx] = cfg.Platform

			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	seen := make(map[string]struct{})
	for _, p := range targets {
		s := platforms.FormatAll(p)
		if _, ok := seen[s]; ok {
			return nil, fmt.Errorf("mutiple base images provided with the same platform value")
		}
		seen[s] = struct{}{}
	}

	dc.TargetPlatforms = targets
	if len(targets) > 1 {
		dc.MultiPlatformRequested = true
	}
	targetKey := frontend.GetTargetKey(client)

	warnBaseOverride := sync.OnceFunc(func() {
		frontend.Warn(ctx, client, llb.Scratch(), "Base image defined in spec overwritten by base images context")
	})

	getBaseRef := func(idx int, spec *dalec.Spec) string {
		baseRef := refs[idx]

		updated := dalec.GetBaseOutputImage(spec, targetKey)
		if updated == "" {
			return baseRef
		}

		if bases.len() == 0 {
			return updated
		}

		warnBaseOverride()
		return baseRef
	}

	rb, err := dcBuild(ctx, dc, func(ctx context.Context, platform *ocispecs.Platform, idx int) (ref gwclient.Reference, retCfg, retBaseCfg *dalec.DockerImageSpec, retErr error) {
		spec, err := frontend.LoadSpec(ctx, dc, platform, frontend.WithAllowArgs(
			argBasesPathKey,
			argBasesContextKey,
		))
		if err != nil {
			return nil, nil, nil, err
		}

		if err := validateRuntimeDeps(spec, targetKey); err != nil {
			return nil, nil, nil, fmt.Errorf("error validating windows spec: %w", err)
		}

		pg := dalec.ProgressGroup("Build windows container: " + spec.Name)
		worker, err := distroConfig.Worker(sOpt, pg)
		if err != nil {
			return nil, nil, nil, err
		}

		bin, err := buildBinaries(ctx, spec, worker, client, sOpt, targetKey)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("unable to build binary %w", err)
		}

		baseRef := getBaseRef(idx, spec)
		baseImage := llb.Image(baseRef, llb.Platform(*platform))

		out := baseImage.
			File(llb.Copy(bin, "/", windowsSystemDir)).
			With(copySymlinks(spec.GetImagePost(targetKey)))

		def, err := out.Marshal(ctx)
		if err != nil {
			return nil, nil, nil, err
		}

		res, err := client.Solve(ctx, gwclient.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, nil, nil, err
		}

		var baseCfg dalec.DockerImageSpec
		if err := json.Unmarshal(cfgs[idx], &baseCfg); err != nil {
			return nil, nil, nil, errors.Wrap(err, "error unmarshalling base image config")
		}

		// Get a copy of the cfg so we can modify it
		var img dalec.DockerImageSpec
		if err := json.Unmarshal(cfgs[idx], &img); err != nil {
			return nil, nil, nil, errors.Wrap(err, "error unmarshalling base image config")
		}

		if err := dalec.BuildImageConfig(spec, targetKey, &img); err != nil {
			return nil, nil, nil, errors.Wrap(err, "error creating image config")
		}

		ref, err = res.SingleRef()
		return ref, &img, &baseCfg, err
	})
	if err != nil {
		return nil, err
	}

	return rb.Finalize()
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
