package distro

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/frontend/rpm"
	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

var (
	defaultRepoConfig = &dnfRepoPlatform
)

func (c *Config) BuildRPM(worker llb.State, ctx context.Context, client gwclient.Client, spec *dalec.Spec, sOpt dalec.SourceOpts, targetKey string, opts ...llb.ConstraintsOpt) (llb.State, error) {
	worker = worker.With(c.InstallBuildDeps(ctx, client, spec, targetKey, opts...))

	br, err := rpm.SpecToBuildrootLLB(worker, spec, sOpt, targetKey, opts...)
	if err != nil {
		return llb.Scratch(), err
	}

	specPath := filepath.Join("SPECS", spec.Name, spec.Name+".spec")

	builder := worker.With(dalec.SetBuildNetworkMode(spec))
	st := rpm.Build(br, builder, specPath, opts...)

	return frontend.MaybeSign(ctx, client, st, spec, targetKey, sOpt, opts...)
}

func (cfg *Config) HandleRPM(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		if err := rpm.ValidateSpec(spec); err != nil {
			return nil, nil, fmt.Errorf("rpm: invalid spec: %w", err)
		}

		pg := dalec.ProgressGroup("Building " + targetKey + " rpm: " + spec.Name)
		sOpt, err := frontend.SourceOptFromClient(ctx, client)
		if err != nil {
			return nil, nil, err
		}

		worker, err := cfg.Worker(sOpt, pg)
		if err != nil {
			return nil, nil, errors.Wrap(err, "error building worker container")
		}

		rpmSt, err := cfg.BuildRPM(worker, ctx, client, spec, sOpt, targetKey, pg)
		if err != nil {
			return nil, nil, err
		}

		def, err := rpmSt.Marshal(ctx, pg)
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

		ctr, err := cfg.BuildContainer(worker, spec, targetKey, rpmSt, sOpt)
		if err != nil {
			return ref, nil, err
		}

		if ref, err := cfg.runTests(ctx, worker, client, spec, sOpt, ctr, targetKey, pg); err != nil {
			cfg, _ := cfg.BuildImageConfig(ctx, client, spec, platform, targetKey)
			return ref, cfg, err
		}

		if platform == nil {
			p := platforms.DefaultSpec()
			platform = &p
		}
		return ref, &dalec.DockerImageSpec{Image: ocispecs.Image{Platform: *platform}}, nil
	})
}

// runTests runs the package tests
// The returned reference is the solved container state
func (cfg *Config) runTests(ctx context.Context, worker llb.State, client gwclient.Client, spec *dalec.Spec, sOpt dalec.SourceOpts, ctr llb.State, targetKey string, opts ...llb.ConstraintsOpt) (gwclient.Reference, error) {
	def, err := ctr.Marshal(ctx, opts...)
	if err != nil {
		return nil, err
	}

	res, err := client.Solve(ctx, gwclient.SolveRequest{
		Definition: def.ToPB(),
	})
	if err != nil {
		return nil, err
	}

	ref, err := res.SingleRef()
	if err != nil {
		return nil, err
	}

	withTestDeps := cfg.InstallTestDeps(worker, sOpt, targetKey, spec, opts...)
	err = frontend.RunTests(ctx, client, spec, ref, withTestDeps, targetKey)
	return ref, errors.Wrap(err, "TESTS FAILED")
}

// TODO(adamperlin): can this implementation be shared between RPM and DEB?
func (cfg *Config) RepoMounts(repos []dalec.PackageRepositoryConfig, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) (llb.RunOption, []string, error) {
	opts = append(opts, dalec.ProgressGroup("Prepare custom repos"))

	repoConfig := cfg.RepoPlatformConfig
	if repoConfig == nil {
		repoConfig = defaultRepoConfig
	}

	withRepos, err := dalec.WithRepoConfigs(repos, repoConfig, sOpt, opts...)
	if err != nil {
		return nil, []string{}, err
	}

	withData, err := dalec.WithRepoData(repos, sOpt, opts...)
	if err != nil {
		return nil, []string{}, err
	}

	keyMounts, keyPaths, err := dalec.GetRepoKeys(repos, repoConfig, sOpt, opts...)
	if err != nil {
		return nil, []string{}, err
	}

	return dalec.WithRunOptions(withRepos, withData, keyMounts), keyPaths, nil
}

// TODO(adamperlin): can this implementation be shared between rpm/deb?
func (cfg *Config) InstallTestDeps(worker llb.State, sOpt dalec.SourceOpts, targetKey string, spec *dalec.Spec, opts ...llb.ConstraintsOpt) llb.StateOption {
	deps := spec.GetTestDeps(targetKey)
	if len(deps) == 0 {
		return func(s llb.State) llb.State { return s }
	}

	return func(in llb.State) llb.State {
		return in.Async(func(ctx context.Context, in llb.State, c *llb.Constraints) (llb.State, error) {
			repos := spec.GetTestRepos(targetKey)

			repoMounts, keyPaths, err := cfg.RepoMounts(repos, sOpt, opts...)
			if err != nil {
				return in, err
			}

			importRepos := []DnfInstallOpt{DnfAtRoot("/tmp/rootfs"), DnfWithMounts(repoMounts), DnfImportKeys(keyPaths)}

			// TODO(adamperlin): proper caching
			opts = append(opts, dalec.ProgressGroup("Install test dependencies"))
			return worker.Run(
				dalec.WithConstraints(opts...),
				cfg.Install(deps, importRepos...),
			).AddMount("/tmp/rootfs", in), nil
		})
	}
}

// No plan to implement right now as HandleRPM should deal with this
func (cfg *Config) HandleSourcePkg(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return nil, nil
}
