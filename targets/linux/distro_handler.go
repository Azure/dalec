package linux

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	"github.com/pkg/errors"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"

	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

type DistroConfig interface {
	// Validate does any distro or packaging-specific validation of a Dalec spec.
	Validate(*dalec.Spec) error

	// Worker returns the worker image for the particular distro
	Worker(sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, error)
	SysextWorker(worker llb.State, opts ...llb.ConstraintsOpt) llb.State

	// BuildPkg returns an llb.State containing the built package
	// which the passed in spec describes. This should be composable with
	// BuildContainer(), which can consume the returned state.
	BuildPkg(ctx context.Context,
		client gwclient.Client,
		worker llb.State,
		sOpt dalec.SourceOpts,
		spec *dalec.Spec, targetKey string, opts ...llb.ConstraintsOpt) (llb.State, error)

	// ExtractPkg consumes an llb.State containing the built package from the
	// given *dalec.Spec, and extracts it in a scratch container, along with any
	// dependencies listed under sysext. The package manager is not used, so no
	// further dependency resolution is performed.
	ExtractPkg(ctx context.Context, client gwclient.Client, worker llb.State, sOpt dalec.SourceOpts,
		spec *dalec.Spec, targetKey string,
		pkgState llb.State, opts ...llb.ConstraintsOpt) llb.State

	// BuildContainer consumes an llb.State containing the built package from the
	// given *dalec.Spec, and installs it in a target container.
	BuildContainer(ctx context.Context, client gwclient.Client, worker llb.State, sOpt dalec.SourceOpts,
		spec *dalec.Spec, targetKey string,
		pkgState llb.State, opts ...llb.ConstraintsOpt) (llb.State, error)

	// RunTests runts the tests specified in a dalec spec against a built container, which may be the target container.
	// Some distros may need to pass in a separate worker before mounting the target container.
	RunTests(ctx context.Context, client gwclient.Client, worker llb.State, spec *dalec.Spec, sOpt dalec.SourceOpts, ctr llb.State,
		targetKey string, opts ...llb.ConstraintsOpt) (gwclient.Reference, error)
}

func BuildImageConfig(ctx context.Context, sOpt dalec.SourceOpts, spec *dalec.Spec, platform *ocispecs.Platform, targetKey string) (*dalec.DockerImageSpec, error) {
	img, err := resolveConfig(ctx, sOpt, spec, platform, targetKey)
	if err != nil {
		return nil, err
	}

	if err := dalec.BuildImageConfig(spec, targetKey, img); err != nil {
		return nil, err
	}

	return img, nil
}

func resolveConfig(ctx context.Context, sOpt dalec.SourceOpts, spec *dalec.Spec, platform *ocispecs.Platform, targetKey string) (*dalec.DockerImageSpec, error) {
	bi, err := spec.GetSingleBase(targetKey)
	if err != nil {
		return nil, err
	}

	if bi == nil {
		return dalec.BaseImageConfig(platform), nil
	}

	dt, err := bi.ResolveImageConfig(ctx, sOpt, sourceresolver.Opt{
		Platform: platform,
	})
	if err != nil {
		return nil, errors.Wrap(err, "error resolving base image config")
	}

	var img dalec.DockerImageSpec
	if err := json.Unmarshal(dt, &img); err != nil {
		return nil, errors.Wrap(err, "error unmarshalling base image config")
	}
	return &img, nil
}

func HandleContainer(c DistroConfig) gwclient.BuildFunc {
	return func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
		return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
			sOpt, err := frontend.SourceOptFromClient(ctx, client, platform)
			if err != nil {
				return nil, nil, err
			}

			var opts []llb.ConstraintsOpt
			opts = append(opts, dalec.ProgressGroup(spec.Name))
			opts = append(opts, dalec.Platform(platform))

			worker, err := c.Worker(sOpt, opts...)
			if err != nil {
				return nil, nil, err
			}

			pkgSt, foundPrebuiltPkg := getPrebuiltPackage(ctx, targetKey, client, opts, sOpt)

			// Pre-built package wasn't found so we need to build it.
			if !foundPrebuiltPkg {
				var err error
				pkgSt, err = c.BuildPkg(ctx, client, worker, sOpt, spec, targetKey, opts...)
				if err != nil {
					return nil, nil, err
				}
			}

			img, err := BuildImageConfig(ctx, sOpt, spec, platform, targetKey)
			if err != nil {
				return nil, nil, err
			}

			ctr, err := c.BuildContainer(ctx, client, worker, sOpt, spec, targetKey, pkgSt, opts...)
			if err != nil {
				return nil, nil, err
			}

			ref, err := c.RunTests(ctx, client, worker, spec, sOpt, ctr, targetKey, opts...)
			return ref, img, err
		})
	}
}

//go:embed build_sysext.sh
var buildSysextSh []byte

func HandleSysext(c DistroConfig) gwclient.BuildFunc {
	return func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
		return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
			sOpt, err := frontend.SourceOptFromClient(ctx, client, platform)
			if err != nil {
				return nil, nil, err
			}

			pc := dalec.Platform(platform)
			var opts []llb.ConstraintsOpt
			opts = append(opts, dalec.ProgressGroup(spec.Name))
			opts = append(opts, pc)

			worker, err := c.Worker(sOpt, opts...)
			if err != nil {
				return nil, nil, errors.Wrap(err, "error building worker container")
			}

			pkgSt, foundPrebuiltPkg := getPrebuiltPackage(ctx, targetKey, client, opts, sOpt)

			// Pre-built package wasn't found so we need to build it.
			if !foundPrebuiltPkg {
				var err error
				pkgSt, err = c.BuildPkg(ctx, client, worker, sOpt, spec, targetKey, opts...)
				if err != nil {
					return nil, nil, err
				}
			}

			extracted := c.ExtractPkg(ctx, client, worker, sOpt, spec, targetKey, pkgSt, opts...)

			if platform == nil {
				p := platforms.DefaultSpec()
				platform = &p
			}

			scriptPath := "/tmp/dalec/internal/sysext/build.sh"

			scriptFile := llb.Scratch().File(
				llb.Mkfile("build_sysext.sh", 0o755, []byte(buildSysextSh)),
				dalec.WithConstraints(opts...),
			)

			rev := spec.Revision
			if rev == "" {
				rev = "1"
			}

			erofs := c.SysextWorker(worker, opts...).Run(
				llb.Args([]string{scriptPath, spec.Name, fmt.Sprintf("v%s-%s-%s", spec.Version, rev, targetKey), platform.Architecture}),
				llb.AddMount(scriptPath, scriptFile, llb.SourcePath("build_sysext.sh"), llb.Readonly),
				llb.AddMount("/input", extracted, llb.Readonly),
				dalec.WithConstraints(opts...),
			).AddMount("/output", llb.Scratch())

			def, err := erofs.Marshal(ctx, pc)
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
			if err := ref.Evaluate(ctx); err != nil {
				return ref, nil, err
			}

			return ref, &dalec.DockerImageSpec{Image: ocispecs.Image{Platform: *platform}}, nil
		})
	}
}

// getPrebuiltPackage retrieves a package based on the target environment.
// Target-specific packages (e.g., "{targetKey}-pkg") are prioritized over generic packages ("pkg").
// This ensures compatibility with the build context and optimizes functionality for specific environments.
// Examples of target keys include "mariner2", "azlinux3", "windowscross", and "bookworm".
func getPrebuiltPackage(ctx context.Context, targetKey string, client gwclient.Client, opts []llb.ConstraintsOpt, sOpt dalec.SourceOpts) (llb.State, bool) {
	var pkgSt llb.State

	// Try target-specific package first.
	targetSpecificName := targetKey + dalec.PreBuiltPkgSuffix
	targetPkgSt, err := sOpt.GetContext(targetSpecificName, dalec.WithConstraints(opts...))
	if err != nil {
		return llb.Scratch().Async(func(ctx context.Context, _ llb.State, _ *llb.Constraints) (llb.State, error) {
			// If attempts failed for retrieving a pre-built package from the build context, surface the error up when the state gets marshalled.
			return pkgSt, fmt.Errorf("error when retrieving target-specified package for %s: %w", targetKey, err)
		}), false
	}
	if targetPkgSt != nil {
		pkgSt = *targetPkgSt
		frontend.Warn(ctx, client, pkgSt, fmt.Sprintf("Using target-specific package from %s context", targetSpecificName))
		return pkgSt, true
	}

	// Try generic package.
	genericPkgSt, err := sOpt.GetContext(dalec.GenericPkg, dalec.WithConstraints(opts...))
	if err != nil {
		return llb.Scratch().Async(func(ctx context.Context, _ llb.State, _ *llb.Constraints) (llb.State, error) {
			// If attempts failed for retrieving a pre-built package from the build context, surface the error up when the state gets marshalled.
			return pkgSt, fmt.Errorf("error when retrieving generic package for %s: %w", targetKey, err)
		}), false
	}
	if genericPkgSt != nil {
		pkgSt = *genericPkgSt
		frontend.Warn(ctx, client, pkgSt, fmt.Sprintf("Fallback to using generic package from %s context", targetSpecificName))
		return pkgSt, true
	}

	return pkgSt, false
}

func HandlePackage(cfg DistroConfig) gwclient.BuildFunc {
	return func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
		return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
			if err := cfg.Validate(spec); err != nil {
				return nil, nil, fmt.Errorf("rpm: invalid spec: %w", err)
			}

			pg := dalec.ProgressGroup("Building " + targetKey + " package: " + spec.Name)
			sOpt, err := frontend.SourceOptFromClient(ctx, client, platform)
			if err != nil {
				return nil, nil, err
			}

			pc := dalec.Platform(platform)
			worker, err := cfg.Worker(sOpt, pg, pc)
			if err != nil {
				return nil, nil, errors.Wrap(err, "error building worker container")
			}

			pkgSt, err := cfg.BuildPkg(ctx, client, worker, sOpt, spec, targetKey, pg, pc)
			if err != nil {
				return nil, nil, err
			}

			def, err := pkgSt.Marshal(ctx, pc)
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
			if err := ref.Evaluate(ctx); err != nil {
				return ref, nil, err
			}

			ctr, err := cfg.BuildContainer(ctx, client, worker, sOpt, spec, targetKey, pkgSt, pg, pc)
			if err != nil {
				return ref, nil, err
			}

			if ref, err := cfg.RunTests(ctx, client, worker, spec, sOpt, ctr, targetKey, pg, pc); err != nil {
				cfg, _ := BuildImageConfig(ctx, sOpt, spec, platform, targetKey)
				return ref, cfg, err
			}

			if platform == nil {
				p := platforms.DefaultSpec()
				platform = &p
			}
			return ref, &dalec.DockerImageSpec{Image: ocispecs.Image{Platform: *platform}}, nil
		})
	}
}
