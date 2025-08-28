package distro

import (
	"context"
	"path/filepath"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/packaging/linux/rpm"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/pkg/errors"
)

var (
	defaultRepoConfig = &dnfRepoPlatform
)

func (c *Config) Validate(spec *dalec.Spec) error {
	if err := rpm.ValidateSpec(spec); err != nil {
		return err
	}

	return nil
}

func addGoCache(info *rpm.CacheInfo) {
	info.Caches = append(info.Caches, dalec.CacheConfig{
		GoBuild: &dalec.GoBuildCache{},
	})
}

func needsAutoGocache(spec *dalec.Spec, targetKey string) bool {
	for _, c := range spec.Build.Caches {
		if c.GoBuild != nil {
			return false
		}
	}

	if !spec.HasGomods() && !dalec.HasGolang(spec, targetKey) {
		return false
	}

	return true
}

func (c *Config) BuildPkg(ctx context.Context, client gwclient.Client, worker llb.State, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, opts ...llb.ConstraintsOpt) (llb.State, error) {
	worker = worker.With(c.InstallBuildDeps(ctx, client, spec, sOpt, targetKey, opts...))

	br, err := rpm.SpecToBuildrootLLB(worker, spec, sOpt, targetKey, opts...)
	if err != nil {
		return llb.Scratch(), err
	}

	specPath := filepath.Join("SPECS", spec.Name, spec.Name+".spec")

	builder := worker.With(dalec.SetBuildNetworkMode(spec))
	cacheInfo := rpm.CacheInfo{TargetKey: targetKey, Caches: spec.Build.Caches}

	if needsAutoGocache(spec, targetKey) {
		addGoCache(&cacheInfo)
	}

	st := rpm.Build(br, builder, specPath, cacheInfo, opts...)

	return frontend.MaybeSign(ctx, client, st, spec, targetKey, sOpt, opts...)
}

// runTests runs the package tests
// The returned reference is the solved container state
func (cfg *Config) RunTests(ctx context.Context, client gwclient.Client, worker llb.State, spec *dalec.Spec, sOpt dalec.SourceOpts, ctr llb.State, targetKey string, opts ...llb.ConstraintsOpt) (gwclient.Reference, error) {
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
	err = frontend.RunTests(ctx, client, spec, ref, withTestDeps, targetKey, sOpt.TargetPlatform)
	return ref, errors.Wrap(err, "TESTS FAILED")
}

func (cfg *Config) RepoMounts(repos []dalec.PackageRepositoryConfig, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) (llb.RunOption, []string) {
	opts = append(opts, dalec.ProgressGroup("Prepare custom repos"))
	repoConfig := cfg.RepoPlatformConfig
	if repoConfig == nil {
		repoConfig = defaultRepoConfig
	}

	withRepos := dalec.WithRepoConfigs(repos, repoConfig, sOpt, opts...)
	withData := dalec.WithRepoData(repos, sOpt, opts...)
	keyMounts, keyPaths := dalec.GetRepoKeys(repos, repoConfig, sOpt, opts...)

	return dalec.WithRunOptions(withRepos, withData, keyMounts), keyPaths
}

func (cfg *Config) InstallTestDeps(worker llb.State, sOpt dalec.SourceOpts, targetKey string, spec *dalec.Spec, opts ...llb.ConstraintsOpt) llb.StateOption {
	deps := spec.GetTestDeps(targetKey)
	if len(deps) == 0 {
		return func(s llb.State) llb.State { return s }
	}

	return func(in llb.State) llb.State {
		repos := spec.GetTestRepos(targetKey)
		repoMounts, keyPaths := cfg.RepoMounts(repos, sOpt, opts...)
		importRepos := []DnfInstallOpt{DnfAtRoot("/tmp/rootfs"), DnfWithMounts(repoMounts), DnfImportKeys(keyPaths)}

		opts = append(opts, dalec.ProgressGroup("Install test dependencies"))
		return worker.Run(
			dalec.WithConstraints(opts...),
			cfg.Install(deps, importRepos...),
		).AddMount("/tmp/rootfs", in)
	}
}

func (cfg *Config) ExtractPkg(ctx context.Context, client gwclient.Client, worker llb.State, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, rpmDir llb.State, opts ...llb.ConstraintsOpt) llb.State {
	deps := cfg.DownloadDeps(worker, sOpt, spec, targetKey, opts...)
	opts = append(opts, dalec.ProgressGroup("Extracting RPMs"))

	return worker.Run(
		llb.Args([]string{"find", "/input", "-name", "*.rpm", "-exec", "sh", "-c", "rpm2cpio \"$1\" | cpio -idmv -D /output", "-", "{}", ";"}),
		llb.AddMount("/input/build", rpmDir, llb.SourcePath("/RPMS")),
		llb.AddMount("/input/deps", deps),
		dalec.WithConstraints(opts...),
	).AddMount("/output", llb.Scratch())
}
