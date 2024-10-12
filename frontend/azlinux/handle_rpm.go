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
			return ref, &dalec.DockerImageSpec{}, nil
		})
	}
}

// Creates and installs an rpm meta-package that requires the passed in deps as runtime-dependencies
func installBuildDepsPackage(target string, packageName string, w worker, deps map[string]dalec.PackageConstraints, keyPaths []string, repoMounts llb.RunOption, installOpts ...installOpt) installFunc {
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
			withMounts(repoMounts),
			importKeys(keyPaths),
			installWithConstraints(opts),
		}, installOpts...)

		// install the built RPMs into the worker itself
		return w.Install([]string{"/tmp/rpms/*/*.rpm"}, installOpts...), nil
	}
}

// meant to return a run option for mounting all repo state
func withRepoData(repos []dalec.PackageRepositoryConfig, sOpts dalec.SourceOpts, opts ...llb.ConstraintsOpt) (llb.RunOption, error) {
	var repoMountsOpts []llb.RunOption
	for _, repo := range repos {
		rs, err := repoDataAsMount(repo, sOpts, opts...)
		if err != nil {
			return nil, err
		}
		repoMountsOpts = append(repoMountsOpts, rs)
	}

	return dalec.WithRunOptions(repoMountsOpts...), nil
}

// meant to return a run option for mounting state for a single repo
func repoDataAsMount(config dalec.PackageRepositoryConfig, sOpts dalec.SourceOpts, opts ...llb.ConstraintsOpt) (llb.RunOption, error) {
	var mounts []llb.RunOption
	for _, data := range config.Data {
		repoState, err := data.Spec.AsMount(data.Dest, sOpts, opts...)
		if err != nil {
			return nil, err
		}
		mounts = append(mounts, llb.AddMount(data.Dest, repoState))
	}

	return dalec.WithRunOptions(mounts...), nil
}

// meant to return a run option for importing the config files for all repos
func withRepoConfig(repos []dalec.PackageRepositoryConfig, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) (llb.RunOption, error) {
	configStates := []llb.RunOption{}
	for _, repo := range repos {
		mnts, err := repoConfigAsMount(repo, sOpt, opts...)
		if err != nil {
			return nil, err
		}

		configStates = append(configStates, mnts...)
	}

	return dalec.WithRunOptions(configStates...), nil
}

func withRepoKeys(configs []dalec.PackageRepositoryConfig, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) (llb.RunOption, []string, error) {
	keys := []llb.RunOption{}
	names := []string{}
	for _, config := range configs {
		for name, repoKey := range config.Keys {
			// each of these sources represent a gpg key file for a particular repo
			gpgKey, err := repoKey.AsState(name, sOpt, append(opts, dalec.ProgressGroup("Importing repo key: "+name))...)
			if err != nil {
				return nil, nil, err
			}

			keys = append(keys, llb.AddMount(filepath.Join("/etc/pki/rpm-gpg", name), gpgKey, llb.SourcePath(name)))
			names = append(names, name)
		}
	}

	return dalec.WithRunOptions(keys...), names, nil
}

func repoConfigAsMount(config dalec.PackageRepositoryConfig, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) ([]llb.RunOption, error) {
	repoConfigs := []llb.RunOption{}
	keys := []llb.RunOption{}

	for name, repoConfig := range config.Config {
		// each of these sources represent a repo config file
		repoConfigSt, err := repoConfig.AsState(name, sOpt, append(opts, dalec.ProgressGroup("Importing repo config: "+name))...)
		if err != nil {
			return nil, err
		}

		repoConfigs = append(repoConfigs,
			llb.AddMount(filepath.Join("/etc/yum.repos.d", name), repoConfigSt, llb.SourcePath(name)))
	}

	for name, repoKey := range config.Keys {
		// each of these sources represent a gpg key file for a particular repo
		gpgKey, err := repoKey.AsState(name, sOpt, append(opts, dalec.ProgressGroup("Importing repo key: "+name))...)
		if err != nil {
			return nil, err
		}

		keys = append(keys, llb.AddMount(filepath.Join("/etc/pki/rpm-gpg", name), gpgKey, llb.SourcePath(name)))
	}

	return append(repoConfigs, keys...), nil
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

	importRepo, err := withRepoConfig(repos, sOpt, opts...)
	if err != nil {
		return nil, err
	}

	repoData, err := withRepoData(repos, sOpt, opts...)
	if err != nil {
		return nil, err
	}

	repoKeys, keyNames, err := withRepoKeys(repos, sOpt)
	if err != nil {
		return nil, err
	}

	opts = append(opts, dalec.ProgressGroup("Install build deps"))
	installOpt, err := installBuildDepsPackage(targetKey, spec.Name, w, deps, keyNames, dalec.WithRunOptions(importRepo, repoData, repoKeys),
		installWithConstraints(opts))(ctx, client, sOpt)
	if err != nil {
		return nil, err
	}

	return func(in llb.State) llb.State {
		return in.
			Run(installOpt, dalec.WithConstraints(opts...)).Root()
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
