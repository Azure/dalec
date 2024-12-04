package distro

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

type PackageInstaller func(*dnfInstallConfig, string, []string) llb.RunOption

type dnfInstallConfig struct {
	// Disables GPG checking when installing RPMs.
	// this is needed when installing unsigned RPMs.
	noGPGCheck bool

	// path for gpg keys to import for using a repo. These files for these keys
	// must also be added as mounts
	keys []string

	// Sets the root path to install rpms too.
	// this acts like installing to a chroot.
	root string

	// Additional mounts to add to the tdnf install command (useful if installing RPMS which are mounted to a local directory)
	mounts []llb.RunOption

	constraints []llb.ConstraintsOpt
}

type DnfInstallOpt func(*dnfInstallConfig)

func DnfNoGPGCheck(cfg *dnfInstallConfig) {
	cfg.noGPGCheck = true
}

// see comment in tdnfInstall for why this additional option is needed
func DnfImportKeys(keys []string) DnfInstallOpt {
	return func(cfg *dnfInstallConfig) {
		cfg.keys = append(cfg.keys, keys...)
	}
}

func DnfWithMounts(opts ...llb.RunOption) DnfInstallOpt {
	return func(cfg *dnfInstallConfig) {
		cfg.mounts = append(cfg.mounts, opts...)
	}
}

func DnfAtRoot(root string) DnfInstallOpt {
	return func(cfg *dnfInstallConfig) {
		cfg.root = root
	}
}

func dnfInstallWithConstraints(opts []llb.ConstraintsOpt) DnfInstallOpt {
	return func(cfg *dnfInstallConfig) {
		cfg.constraints = opts
	}
}

func dnfInstallFlags(cfg *dnfInstallConfig) string {
	var cmdOpts string

	if cfg.noGPGCheck {
		cmdOpts += " --nogpgcheck"
	}

	if cfg.root != "" {
		cmdOpts += " --installroot=" + cfg.root
		cmdOpts += " --setopt=reposdir=/etc/yum.repos.d"
	}

	return cmdOpts
}

func dnfInstallOptions(cfg *dnfInstallConfig, opts []DnfInstallOpt) {
	for _, o := range opts {
		o(cfg)
	}
}

func TdnfInstall(cfg *dnfInstallConfig, releaseVer string, pkgs []string) llb.RunOption {
	cmdFlags := dnfInstallFlags(cfg)
	// tdnf makecache is needed to ensure that the package metadata is up to date if extra repo
	// config files have been mounted
	cmdArgs := fmt.Sprintf("set -ex; tdnf makecache; tdnf install -y --refresh --releasever=%s %s %s", releaseVer, cmdFlags, strings.Join(pkgs, " "))

	var runOpts []llb.RunOption

	// TODO: see if this can be removed for dnf
	// If we have keys to import in order to access a repo, we need to create a script to use `gpg` to import them
	// This is an unfortunate consequence of a bug in tdnf (see https://github.com/vmware/tdnf/issues/471).
	// The keys *should* be imported automatically by tdnf as long as the repo config references them correctly and
	// we mount the key files themselves under the right path. However, tdnf does NOT do this
	// currently if the keys are referenced via a `file:///` type url,
	// and we must manually import the keys as well.
	if len(cfg.keys) > 0 {
		importScript := importGPGScript(cfg.keys)
		cmdArgs = "/tmp/import-keys.sh; " + cmdArgs
		runOpts = append(runOpts, llb.AddMount("/tmp/import-keys.sh",
			llb.Scratch().File(llb.Mkfile("/import-keys.sh", 0755, []byte(importScript))),
			llb.SourcePath("/import-keys.sh")))
	}

	runOpts = append(runOpts, dalec.ShArgs(cmdArgs))
	runOpts = append(runOpts, cfg.mounts...)

	return dalec.WithRunOptions(runOpts...)

}

func DnfInstall(cfg *dnfInstallConfig, releaseVer string, pkgs []string) llb.RunOption {
	cmdFlags := dnfInstallFlags(cfg)
	// tdnf makecache is needed to ensure that the package metadata is up to date if extra repo
	// config files have been mounted
	cmdArgs := fmt.Sprintf("set -ex; dnf makecache; dnf install -y --refresh --releasever=%s %s %s", releaseVer, cmdFlags, strings.Join(pkgs, " "))

	var runOpts []llb.RunOption

	// TODO: see if this can be removed for dnf
	// If we have keys to import in order to access a repo, we need to create a script to use `gpg` to import them
	// This is an unfortunate consequence of a bug in tdnf (see https://github.com/vmware/tdnf/issues/471).
	// The keys *should* be imported automatically by tdnf as long as the repo config references them correctly and
	// we mount the key files themselves under the right path. However, tdnf does NOT do this
	// currently if the keys are referenced via a `file:///` type url,
	// and we must manually import the keys as well.
	if len(cfg.keys) > 0 {
		importScript := importGPGScript(cfg.keys)
		cmdArgs = "/tmp/import-keys.sh; " + cmdArgs
		runOpts = append(runOpts, llb.AddMount("/tmp/import-keys.sh",
			llb.Scratch().File(llb.Mkfile("/import-keys.sh", 0755, []byte(importScript))),
			llb.SourcePath("/import-keys.sh")))
	}

	runOpts = append(runOpts, dalec.ShArgs(cmdArgs))
	runOpts = append(runOpts, cfg.mounts...)

	return dalec.WithRunOptions(runOpts...)
}

var azlinuxRepoPlatform = dalec.RepoPlatformConfig{
	ConfigRoot: "/etc/yum.repos.d",
	GPGKeyRoot: "/etc/pki/rpm-gpg",
	ConfigExt:  ".repo",
}

func repoMountInstallOpts(repos []dalec.PackageRepositoryConfig, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) ([]DnfInstallOpt, error) {
	withRepos, err := dalec.WithRepoConfigs(repos, &azlinuxRepoPlatform, sOpt, opts...)
	if err != nil {
		return nil, err
	}

	withData, err := dalec.WithRepoData(repos, sOpt, opts...)
	if err != nil {
		return nil, err
	}

	keyMounts, keyPaths, err := dalec.GetRepoKeys(repos, &azlinuxRepoPlatform, sOpt, opts...)
	if err != nil {
		return nil, err
	}

	repoMounts := dalec.WithRunOptions(withRepos, withData, keyMounts)
	return []DnfInstallOpt{DnfWithMounts(repoMounts), DnfImportKeys(keyPaths)}, nil
}

func (c *Config) installBuildDepsPackage(target string, packageName string, deps map[string]dalec.PackageConstraints, installOpts ...DnfInstallOpt) installFunc {
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

		installOpts = append([]DnfInstallOpt{
			DnfNoGPGCheck,
			DnfWithMounts(llb.AddMount(rpmMountDir, rpmDir, llb.SourcePath("/RPMS"))),
			dnfInstallWithConstraints(opts),
		}, installOpts...)

		// install the built RPMs into the worker itself
		return w.Install([]string{"/tmp/rpms/*/*.rpm"}, installOpts...), nil
	}
}

func (c *Config) InstallBuildDeps(ctx context.Context, client gwclient.Client, spec *dalec.Spec, targetKey string, opts ...llb.ConstraintsOpt) (llb.StateOption, error) {
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
	installOpt, err := c.installBuildDepsPackage(targetKey, spec.Name, w, deps,
		append(importRepos, dnfInstallWithConstraints(opts))...)(ctx, client, sOpt)
	if err != nil {
		return nil, err
	}

	return func(in llb.State) llb.State {
		return in.Run(installOpt, dalec.WithConstraints(opts...)).Root()
	}, nil
}
