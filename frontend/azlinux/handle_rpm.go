package azlinux

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/frontend/rpm"
	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

func handleRPM(w worker) gwclient.BuildFunc {
	return func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
		return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
			if err := rpm.ValidateSpec(spec); err != nil {
				return nil, nil, fmt.Errorf("rpm: invalid spec: %w", err)
			}

			pg := dalec.ProgressGroup("Building " + targetKey + " rpm: " + spec.Name)
			sOpt, err := frontend.SourceOptFromClient(ctx, client)
			if err != nil {
				return nil, nil, err
			}

			st, err := buildOutputRPM(ctx, w, client, spec, sOpt, targetKey, platform, pg)
			if err != nil {
				return nil, nil, err
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
			return ref, &dalec.DockerImageSpec{}, nil
		})
	}
}

func platformFuzzyMatches(p *ocispecs.Platform) bool {
	if p == nil {
		return true
	}

	// Note, this is intentionally not doing a strict match here
	// (e.g. [platforms.OnlyStrict])
	// This is used to see if we can get some optimizations when building for a
	// non-native platformm and in most cases the [platforms.Only] vector handles
	// things like building armv7 on an arm64 machine, which should be able to run
	// natively.
	return platforms.Only(platforms.DefaultSpec()).Match(*p)
}

func installBuildDeps(w worker, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, platform *ocispecs.Platform, opts ...llb.ConstraintsOpt) (llb.StateOption, error) {
	deps := spec.GetBuildDeps(targetKey)
	if len(deps) == 0 {
		return func(in llb.State) llb.State { return in }, nil
	}

	opts = append(opts, dalec.ProgressGroup("Install build deps"))

	// depsOnly is a simple dalec spec that only includes build dependencies and their constraints
	depsOnly := dalec.Spec{
		Name:        spec.Name + "-build-dependencies",
		Description: "Provides build dependencies for mariner2 and azlinux3",
		Version:     "1.0",
		License:     "Apache 2.0",
		Revision:    "1",
		Dependencies: &dalec.PackageDependencies{
			Runtime: deps,
		},
	}

	// create an RPM with just the build dependencies, using our same base worker
	rpmDir, err := createRPM(w, sOpt, &depsOnly, targetKey, platform, opts...)
	if err != nil {
		return nil, err
	}

	rpmMountDir := "/tmp/rpms"
	pkg := []string{"/tmp/rpms/*/*.rpm"}

	if !platformFuzzyMatches(platform) {
		base, err := w.Base(sOpt, opts...)
		if err != nil {
			return nil, err
		}

		return func(in llb.State) llb.State {
			return base.Run(
				w.Install(
					pkg,
					withMounts(llb.AddMount(rpmMountDir, rpmDir, llb.SourcePath("/RPMS"))),
					atRoot("/tmp/rootfs"),
					withPlatform(platform),
				),
			).AddMount("/tmp/rootfs", in)
		}, nil
	}

	return func(in llb.State) llb.State {
		return in.Run(
			w.Install(
				[]string{"/tmp/rpms/*/*.rpm"},
				withMounts(llb.AddMount(rpmMountDir, rpmDir, llb.SourcePath("/RPMS"))),
				installWithConstraints(opts),
			),
			dalec.WithConstraints(opts...),
		).Root()
	}, nil
}

func rpmWorker(w worker, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, platform *ocispecs.Platform, opts ...llb.ConstraintsOpt) (llb.State, error) {
	base, err := w.Base(sOpt, append(opts, dalec.WithPlatform(platform))...)
	if err != nil {
		return llb.Scratch(), err
	}

	installDeps, err := installBuildDeps(w, sOpt, spec, targetKey, platform, opts...)
	if err != nil {
		return llb.Scratch(), err
	}

	base = base.With(installDeps)
	return base, nil
}

func createBuildroot(w worker, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, opts ...llb.ConstraintsOpt) (llb.State, error) {
	opts = append(opts, dalec.ProgressGroup("Prepare rpm build root: "+spec.Name))

	// Always generate the build root using the native platform
	// There is nothing it does that should require the requested target platform
	native, err := w.Base(sOpt, opts...)
	if err != nil {
		return llb.Scratch(), err
	}

	if spec.HasGomods() {
		// Since the spec has go mods in it, we need to make sure we have go
		// installed in the image.
		install, err := installBuildDeps(w, sOpt, spec, targetKey, nil, opts...)
		if err != nil {
			return llb.Scratch(), err
		}

		native = native.With(install)
	}

	return rpm.SpecToBuildrootLLB(native, spec, sOpt, targetKey, opts...)
}

func nativeGoMount(native llb.State, p *ocispecs.Platform) llb.RunOption {
	const (
		gorootPath      = "/usr/lib/golang"
		goBinPath       = "/usr/bin/go"
		internalBinPath = "/tmp/internal/dalec/bin"
	)

	runOpts := []llb.RunOption{
		llb.AddMount(gorootPath, native, llb.SourcePath(gorootPath), llb.Readonly),
		llb.AddEnv("GOARCH", p.Architecture),
		dalec.RunOptFunc(func(ei *llb.ExecInfo) {
			if p.Variant != "" {
				switch p.Architecture {
				case "arm":
					// GOARM cannot have the `v` prefix that would be in the platform struct
					llb.AddEnv("GOARM", strings.TrimPrefix(p.Variant, "v")).SetRunOption(ei)
				case "amd64":
					// Unlike GOARM, GOAMD64 must have the `v` prefix (Which should be
					// present in the platform struct)
					llb.AddEnv("GOAMD64", p.Variant).SetRunOption(ei)
				default:
					// go does not support any other special sub-architectures currently.
				}
			}
		}),
	}

	return dalec.WithRunOptions(runOpts...)
}

func hasGolangBuildDep(spec *dalec.Spec, targetKey string) bool {
	deps := spec.GetBuildDeps(targetKey)
	for pkg := range deps {
		if pkg == "golang" || pkg == "msft-golang" {
			return true
		}
	}
	return false
}

func platformOrDefault(p *ocispecs.Platform) ocispecs.Platform {
	if p == nil {
		return platforms.DefaultSpec()
	}
	return *p
}

func createRPM(w worker, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, platform *ocispecs.Platform, opts ...llb.ConstraintsOpt) (llb.State, error) {
	br, err := createBuildroot(w, sOpt, spec, targetKey, opts...)
	if err != nil {
		return llb.Scratch(), errors.Wrap(err, "error creating rpm build root")
	}

	base, err := rpmWorker(w, sOpt, spec, targetKey, platform, opts...)
	if err != nil {
		return llb.Scratch(), nil
	}

	var runOpts []llb.RunOption
	if hasGolangBuildDep(spec, targetKey) {
		if !platformFuzzyMatches(platform) {
			native, err := rpmWorker(w, sOpt, spec, targetKey, nil, opts...)
			if err != nil {
				return llb.Scratch(), err
			}

			runOpts = append(runOpts, nativeGoMount(native, platform))
		}

		const goCacheDir = "/tmp/dalec/internal/gocache"
		runOpts = append(runOpts, llb.AddEnv("GOCACHE", goCacheDir))

		// Unfortunately, go cannot invalidate caches for cgo (rather, cgo with 'include' directives).
		// As such we need to include the platform in our cache key.
		cacheKey := targetKey + "-golang-" + platforms.Format(platformOrDefault(platform))
		runOpts = append(runOpts, llb.AddMount(goCacheDir, llb.Scratch(), llb.AsPersistentCacheDir(cacheKey, llb.CacheMountShared)))
	}

	specPath := filepath.Join("SPECS", spec.Name, spec.Name+".spec")
	opts = append(opts, dalec.ProgressGroup("Create RPM: "+spec.Name))
	return rpm.Build(br, base, specPath, runOpts, opts...), nil
}

func buildOutputRPM(ctx context.Context, w worker, client gwclient.Client, spec *dalec.Spec, sOpt dalec.SourceOpts, targetKey string, platform *ocispecs.Platform, opts ...llb.ConstraintsOpt) (llb.State, error) {
	st, err := createRPM(w, sOpt, spec, targetKey, platform, opts...)
	if err != nil {
		return llb.Scratch(), err
	}
	return frontend.MaybeSign(ctx, client, st, spec, targetKey, sOpt)
}
