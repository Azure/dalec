package azlinux

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/frontend/rpm"
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

type installFunc func(dalec.SourceOpts) (llb.RunOption, error)

// Creates and installs an rpm meta-package that requires the passed in deps as runtime-dependencies
func installBuildDepsPackage(target string, packageName string, w worker, sOpt dalec.SourceOpts, deps map[string]dalec.PackageConstraints, platform *ocispecs.Platform, installOpts ...installOpt) installFunc {
	// depsOnly is a simple dalec spec that only includes build dependencies and their constraints
	depsOnly := dalec.Spec{
		Name:        fmt.Sprintf("%s-build-dependencies", packageName),
		Description: "Provides build dependencies for mariner2 and azlinux3",
		Version:     "1.0",
		License:     "Apache 2.0",
		Revision:    "1",
		Dependencies: &dalec.PackageDependencies{
			Runtime: deps,
		},
	}

	return func(Opt dalec.SourceOpts) (llb.RunOption, error) {
		pg := dalec.ProgressGroup("Building container for build dependencies")

		// create an RPM with just the build dependencies, using our same base worker
		rpmDir, err := createRPM(w, sOpt, &depsOnly, target, platform, pg)
		if err != nil {
			return nil, err
		}

		var opts []llb.ConstraintsOpt
		opts = append(opts, dalec.ProgressGroup("Install build deps"))

		rpmMountDir := "/tmp/rpms"

		installOpts = append([]installOpt{
			noGPGCheck,
			withMounts(llb.AddMount(rpmMountDir, rpmDir, llb.SourcePath("/RPMS"))),
			installWithConstraints(opts),
		}, installOpts...)

		// install the built RPMs into the worker itself
		return w.Install([]string{"/tmp/rpms/*/*.rpm"}, installOpts...), nil
	}
}

func installBuildDeps(w worker, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, platform *ocispecs.Platform, opts ...llb.ConstraintsOpt) (llb.StateOption, error) {
	deps := spec.GetBuildDeps(targetKey)
	if len(deps) == 0 {
		return func(in llb.State) llb.State { return in }, nil
	}

	opts = append(opts, dalec.ProgressGroup("Install build deps"))

	installOpt, err := installBuildDepsPackage(targetKey, spec.Name, w, sOpt, deps, platform, installWithConstraints(opts))(sOpt)
	if err != nil {
		return nil, err
	}

	return func(in llb.State) llb.State {
		return in.Run(installOpt, dalec.WithConstraints(opts...)).Root()
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

func createRPM(w worker, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, platform *ocispecs.Platform, opts ...llb.ConstraintsOpt) (llb.State, error) {
	br, err := createBuildroot(w, sOpt, spec, targetKey, opts...)
	if err != nil {
		return llb.Scratch(), errors.Wrap(err, "error creating rpm build root")
	}

	base, err := rpmWorker(w, sOpt, spec, targetKey, platform, opts...)
	if err != nil {
		return llb.Scratch(), nil
	}

	specPath := filepath.Join("SPECS", spec.Name, spec.Name+".spec")
	opts = append(opts, dalec.ProgressGroup("Create RPM: "+spec.Name))
	return rpm.Build(br, base, specPath, opts...), nil
}

func buildOutputRPM(ctx context.Context, w worker, client gwclient.Client, spec *dalec.Spec, sOpt dalec.SourceOpts, targetKey string, platform *ocispecs.Platform, opts ...llb.ConstraintsOpt) (llb.State, error) {
	st, err := createRPM(w, sOpt, spec, targetKey, platform, opts...)
	if err != nil {
		return llb.Scratch(), err
	}
	return frontend.MaybeSign(ctx, client, st, spec, targetKey, sOpt)
}
