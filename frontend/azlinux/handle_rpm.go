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

			st, err := specToRpmLLB(ctx, w, client, spec, sOpt, targetKey, pg)
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

			if imgRef, err := runTests(ctx, client, w, spec, sOpt, st, targetKey, pg); err != nil {
				// return the container ref in case of error so it can be used to debug
				// the installed package state.
				cfg, _ := resolveBaseConfig(ctx, w, client, platform, spec, targetKey)
				return imgRef, cfg, err
			}

			return ref, &dalec.DockerImageSpec{}, nil
		})
	}
}

// runTests runs the package tests
// The returned reference is the solved container state
func runTests(ctx context.Context, client gwclient.Client, w worker, spec *dalec.Spec, sOpt dalec.SourceOpts, rpmDir llb.State, targetKey string, opts ...llb.ConstraintsOpt) (gwclient.Reference, error) {
	withDeps, err := withTestDeps(w, spec, sOpt, targetKey)
	if err != nil {
		return nil, err
	}

	imgSt, err := specToContainerLLB(w, spec, targetKey, rpmDir, sOpt, opts...)
	if err != nil {
		return nil, errors.Wrap(err, "error creating container image state")
	}

	def, err := imgSt.Marshal(ctx, opts...)
	if err != nil {
		return nil, err
	}

	res, err := client.Solve(ctx, gwclient.SolveRequest{Definition: def.ToPB()})
	if err != nil {
		return nil, errors.Wrap(err, "error solving container state")
	}

	ref, err := res.SingleRef()
	if err != nil {
		return nil, err
	}

	err = frontend.RunTests(ctx, client, spec, ref, withDeps, targetKey)
	return ref, errors.Wrap(err, "TESTS FAILED")
}

var azLinuxRepoConfig = dalec.RepoPlatformConfig{
	ConfigRoot: "/etc/yum.repos.d",
	GPGKeyRoot: "/etc/pki/rpm-gpg",
}

func repoMountInstallOpts(repos []dalec.PackageRepositoryConfig, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) ([]installOpt, error) {
	withRepos, err := dalec.WithRepoConfigs(repos, &azLinuxRepoConfig, sOpt, opts...)
	if err != nil {
		return nil, err
	}

	withData, err := dalec.WithRepoData(repos, sOpt, opts...)
	if err != nil {
		return nil, err
	}

	keyMounts, keyPaths, err := dalec.GetRepoKeys(repos, &azLinuxRepoConfig, sOpt, opts...)
	if err != nil {
		return nil, err
	}

	repoMounts := dalec.WithRunOptions(withRepos, withData, keyMounts)
	return []installOpt{withMounts(repoMounts), importKeys(keyPaths)}, nil
}

func withTestDeps(w worker, spec *dalec.Spec, sOpt dalec.SourceOpts, targetKey string, opts ...llb.ConstraintsOpt) (llb.StateOption, error) {
	base, err := w.Base(sOpt, opts...)
	if err != nil {
		return nil, err
	}

	testRepos := spec.GetTestRepos(targetKey)
	importRepos, err := repoMountInstallOpts(testRepos, sOpt, opts...)
	if err != nil {
		return nil, err
	}

	return func(in llb.State) llb.State {
		deps := spec.GetTestDeps(targetKey)
		if len(deps) == 0 {
			return in
		}

		installOpts := []installOpt{atRoot("/tmp/rootfs")}
		return base.Run(
			w.Install(deps, append(installOpts, importRepos...)...),
			dalec.WithConstraints(opts...),
			dalec.ProgressGroup("Install test dependencies"),
		).AddMount("/tmp/rootfs", in)

	}, nil
}

// Creates and installs an rpm meta-package that requires the passed in deps as runtime-dependencies
func installBuildDepsPackage(target string, packageName string, w worker, deps map[string]dalec.PackageConstraints, installOpts ...installOpt) installFunc {
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

	return func(ctx context.Context, client gwclient.Client, sOpt dalec.SourceOpts) (llb.RunOption, error) {
		pg := dalec.ProgressGroup("Building container for build dependencies")

		// create an RPM with just the build dependencies, using our same base worker
		rpmDir, err := specToRpmLLB(ctx, w, client, &depsOnly, sOpt, target, pg)
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

func installBuildDeps(ctx context.Context, w worker, client gwclient.Client, spec *dalec.Spec, targetKey string, opts ...llb.ConstraintsOpt) (llb.StateOption, error) {
	deps := spec.GetBuildDeps(targetKey)
	if len(deps) == 0 {
		return func(in llb.State) llb.State { return in }, nil
	}

	repos := spec.GetBuildRepos(targetKey)

	sOpt, err := frontend.SourceOptFromClient(ctx, client)
	if err != nil {
		return nil, err
	}

	importRepos, err := repoMountInstallOpts(repos, sOpt, opts...)
	if err != nil {
		return nil, err
	}

	opts = append(opts, dalec.ProgressGroup("Install build deps"))
	installOpt, err := installBuildDepsPackage(targetKey, spec.Name, w, deps,
		append(importRepos, installWithConstraints(opts))...)(ctx, client, sOpt)
	if err != nil {
		return nil, err
	}

	return func(in llb.State) llb.State {
		return in.Run(installOpt, dalec.WithConstraints(opts...)).Root()
	}, nil
}

func specToRpmLLB(ctx context.Context, w worker, client gwclient.Client, spec *dalec.Spec, sOpt dalec.SourceOpts, targetKey string, opts ...llb.ConstraintsOpt) (llb.State, error) {
	base, err := w.Base(sOpt, opts...)
	if err != nil {
		return llb.Scratch(), err
	}

	installOpt, err := installBuildDeps(ctx, w, client, spec, targetKey, opts...)
	if err != nil {
		return llb.Scratch(), err
	}
	base = base.With(installOpt)

	br, err := rpm.SpecToBuildrootLLB(base, spec, sOpt, targetKey, opts...)
	if err != nil {
		return llb.Scratch(), err
	}
	specPath := filepath.Join("SPECS", spec.Name, spec.Name+".spec")
	st := rpm.Build(br, base, specPath, opts...)

	return frontend.MaybeSign(ctx, client, st, spec, targetKey, sOpt)
}
