package distro

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/packaging/linux/rpm"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

var dnfRepoPlatform = dalec.RepoPlatformConfig{
	ConfigRoot: "/etc/yum.repos.d",
	GPGKeyRoot: "/etc/pki/rpm-gpg",
	ConfigExt:  ".repo",
}

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

	// Additional mounts to add to the (t?)dnf install command (useful if installing RPMS which are mounted to a local directory)
	mounts []llb.RunOption

	constraints []llb.ConstraintsOpt

	downloadOnly bool

	allDeps bool

	downloadDir string

	// When true, don't omit docs from the installed RPMs.
	includeDocs bool
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

func DnfDownloadAllDeps(dest string) DnfInstallOpt {
	return func(cfg *dnfInstallConfig) {
		cfg.downloadOnly = true
		cfg.allDeps = true
		cfg.downloadDir = dest
	}
}

func IncludeDocs(v bool) DnfInstallOpt {
	return func(cfg *dnfInstallConfig) {
		cfg.includeDocs = v
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

	if cfg.downloadOnly {
		cmdOpts += " --downloadonly"
	}

	if cfg.allDeps {
		cmdOpts += " --alldeps"
	}

	if cfg.downloadDir != "" {
		cmdOpts += " --downloaddir " + cfg.downloadDir
	}

	if !cfg.includeDocs {
		cmdOpts += " --setopt='tsflags=nodocs'"
	}

	return cmdOpts
}

func dnfInstallOptions(cfg *dnfInstallConfig, opts []DnfInstallOpt) {
	for _, o := range opts {
		o(cfg)
	}
}

func importGPGScript(keyPaths []string) string {
	// all keys that are included should be mounted under this path
	keyRoot := "/etc/pki/rpm-gpg"

	importScript := "#!/usr/bin/env sh\nset -eux\n"
	for _, keyPath := range keyPaths {
		keyName := filepath.Base(keyPath)
		importScript += fmt.Sprintf("gpg --import %s\n", filepath.Join(keyRoot, keyName))
	}

	return importScript
}

func dnfCommand(cfg *dnfInstallConfig, releaseVer string, exe string, dnfShArgs []string, dnfArgs []string) llb.RunOption {
	cmdFlags := dnfInstallFlags(cfg)
	// dnf makecache is needed to ensure that the package metadata is up to date if extra repo
	// config files have been mounted
	cmdArgs := fmt.Sprintf(
		"set -ex; %s makecache -y; exec %s -y --refresh --setopt=varsdir=/etc/dnf/vars --releasever=%s %s %s \"${@}\"",
		exe,
		exe,
		releaseVer,
		cmdFlags,
		strings.Join(dnfShArgs, " "),
	)

	var runOpts []llb.RunOption

	// TODO(adamperlin): see if this can be removed for dnf
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

	sh := []string{"sh", "-c", cmdArgs, "-"}
	sh = append(sh, dnfArgs...)

	runOpts = append(runOpts, llb.Args(sh))
	runOpts = append(runOpts, cfg.mounts...)

	return dalec.WithRunOptions(runOpts...)
}

func DnfInstall(cfg *dnfInstallConfig, releaseVer string, pkgs []string) llb.RunOption {
	return dnfCommand(cfg, releaseVer, "dnf", append([]string{"install"}, pkgs...), nil)
}

func TdnfInstall(cfg *dnfInstallConfig, releaseVer string, pkgs []string) llb.RunOption {
	return dnfCommand(cfg, releaseVer, "tdnf", append([]string{"install"}, pkgs...), nil)
}

type buildDepsInstallerFunc func(context.Context, gwclient.Client, dalec.SourceOpts) (llb.RunOption, error)

func (cfg *Config) installBuildDepsPackage(worker llb.State, target string, packageName string, deps map[string]dalec.PackageConstraints, installOpts ...DnfInstallOpt) buildDepsInstallerFunc {
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
		rpmDir, err := cfg.BuildPkg(ctx, client, worker, sOpt, &depsOnly, target, pg)
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
		return cfg.Install([]string{"/tmp/rpms/*/*.rpm"}, installOpts...), nil
	}
}

func (cfg *Config) InstallBuildDeps(ctx context.Context, client gwclient.Client, spec *dalec.Spec, sOpt dalec.SourceOpts, targetKey string, opts ...llb.ConstraintsOpt) llb.StateOption {
	deps := spec.GetBuildDeps(targetKey)
	if len(deps) == 0 {
		return dalec.NoopStateOption
	}

	repos := spec.GetBuildRepos(targetKey)

	sOpt, err := frontend.SourceOptFromClient(ctx, client, sOpt.TargetPlatform)
	if err != nil {
		return dalec.ErrorStateOption(err)
	}

	return func(in llb.State) llb.State {
		repoMounts, keyPaths := cfg.RepoMounts(repos, sOpt, opts...)
		importRepos := []DnfInstallOpt{DnfWithMounts(repoMounts), DnfImportKeys(keyPaths)}

		opts = append(opts, dalec.ProgressGroup("Install build deps"))
		installOpt, err := cfg.installBuildDepsPackage(in, targetKey, spec.Name, deps,
			append(importRepos, dnfInstallWithConstraints(opts))...)(ctx, client, sOpt)
		if err != nil {
			return dalec.ErrorState(in, err)
		}

		return in.Run(installOpt, dalec.WithConstraints(opts...)).Root()
	}
}

func (cfg *Config) DownloadDeps(worker llb.State, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, constraints map[string]dalec.PackageConstraints, opts ...llb.ConstraintsOpt) llb.State {
	if constraints == nil {
		return llb.Scratch()
	}

	opts = append(opts, dalec.ProgressGroup("Downloading dependencies"))

	worker = worker.Run(
		dalec.WithConstraints(opts...),
		cfg.Install([]string{"dnf-utils"}),
	).Root()

	args := []string{"--downloaddir", "/output", "download"}
	for name, constraint := range constraints {
		if len(constraint.Version) == 0 {
			args = append(args, name)
			continue
		}
		for _, version := range constraint.Version {
			args = append(args, fmt.Sprintf("%s %s", name, rpm.FormatVersionConstraint(version)))
		}
	}

	installTimeRepos := spec.GetInstallRepos(targetKey)
	repoMounts, keyPaths := cfg.RepoMounts(installTimeRepos, sOpt, opts...)

	installOpts := []DnfInstallOpt{
		DnfWithMounts(repoMounts),
		DnfImportKeys(keyPaths),
		dnfInstallWithConstraints(opts),
	}

	var installCfg dnfInstallConfig
	dnfInstallOptions(&installCfg, installOpts)

	return worker.Run(
		dalec.WithRunOptions(dnfCommand(&installCfg, cfg.ReleaseVer, "dnf", nil, args)),
		dalec.WithConstraints(opts...),
	).AddMount("/output", llb.Scratch())
}
