package distro

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/packaging/linux/rpm"
	"github.com/moby/buildkit/client/llb"
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
		cmdOpts += " --setopt=tsflags=nodocs"
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
		fullPath := filepath.Join(keyRoot, keyName)
		// rpm --import requires armored keys, check if key is armored and convert if needed
		importScript += fmt.Sprintf(`
if head -1 %s | grep -q 'BEGIN PGP PUBLIC KEY BLOCK'; then
  gpg --import %s
  rpm --import %s
else
  # Import to gpg
  gpg --import %s
  # Export to file then import to rpm (workaround for gpg export to stdout issues)
  gpg --armor --export > /tmp/key.asc
  rpm --import /tmp/key.asc
fi
`, fullPath, fullPath, fullPath, fullPath)
	}

	return importScript
}

func dnfCommand(cfg *dnfInstallConfig, releaseVer string, exe string, dnfSubCmd []string, dnfArgs []string) llb.RunOption {
	const importKeysPath = "/tmp/dalec/internal/dnf/import-keys.sh"

	cacheDir := "/var/cache/" + exe
	if cfg.root != "" {
		cacheDir = filepath.Join(cfg.root, cacheDir)
	}
	installFlags := dnfInstallFlags(cfg)
	installFlags += " -y --setopt varsdir=/etc/dnf/vars --releasever=" + releaseVer + " "
	installScriptDt := `#!/usr/bin/env bash
set -eux -o pipefail

import_keys_path="` + importKeysPath + `"
cmd="` + exe + `"
install_flags="` + installFlags + `"
dnf_sub_cmd="` + strings.Join(dnfSubCmd, " ") + `"
cache_dir="` + cacheDir + `"

if [ -x "$import_keys_path" ]; then
	"$import_keys_path"
fi

$cmd $dnf_sub_cmd $install_flags "${@}"
`
	var runOpts []llb.RunOption

	installScript := llb.Scratch().File(llb.Mkfile("install.sh", 0o700, []byte(installScriptDt)), cfg.constraints...)
	const installScriptPath = "/tmp/dalec/internal/dnf/install.sh"

	runOpts = append(runOpts, llb.AddMount(installScriptPath, installScript, llb.SourcePath("install.sh"), llb.Readonly))

	// TODO(adamperlin): see if this can be removed for dnf
	// If we have keys to import in order to access a repo, we need to create a script to use `gpg` to import them
	// This is an unfortunate consequence of a bug in tdnf (see https://github.com/vmware/tdnf/issues/471).
	// The keys *should* be imported automatically by tdnf as long as the repo config references them correctly and
	// we mount the key files themselves under the right path. However, tdnf does NOT do this
	// currently if the keys are referenced via a `file:///` type url,
	// and we must manually import the keys as well.
	if len(cfg.keys) > 0 {
		importScript := importGPGScript(cfg.keys)
		runOpts = append(runOpts, llb.AddMount(importKeysPath,
			llb.Scratch().File(llb.Mkfile("/import-keys.sh", 0755, []byte(importScript))),
			llb.Readonly,
			llb.SourcePath("/import-keys.sh")))
	}

	cmd := make([]string, 0, len(dnfArgs)+1)
	cmd = append(cmd, installScriptPath)
	cmd = append(cmd, dnfArgs...)

	runOpts = append(runOpts, llb.Args(cmd))
	runOpts = append(runOpts, cfg.mounts...)

	return dalec.WithRunOptions(runOpts...)
}

func DnfInstall(cfg *dnfInstallConfig, releaseVer string, pkgs []string) llb.RunOption {
	return dnfCommand(cfg, releaseVer, "dnf", append([]string{"install"}, pkgs...), nil)
}

func TdnfInstall(cfg *dnfInstallConfig, releaseVer string, pkgs []string) llb.RunOption {
	return dnfCommand(cfg, releaseVer, "tdnf", append([]string{"install"}, pkgs...), nil)
}

func (cfg *Config) InstallBuildDeps(spec *dalec.Spec, sOpt dalec.SourceOpts, targetKey string, opts ...llb.ConstraintsOpt) llb.StateOption {
	deps := spec.GetPackageDeps(targetKey).GetBuild()
	if len(deps) == 0 {
		return dalec.NoopStateOption
	}
	repos := spec.GetBuildRepos(targetKey)
	return cfg.WithDeps(sOpt, targetKey, spec.Name, deps, repos, opts...)
}

func (cfg *Config) WithDeps(sOpt dalec.SourceOpts, targetKey, pkgName string, deps dalec.PackageDependencyList, repos []dalec.PackageRepositoryConfig, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		if len(deps) == 0 {
			return in
		}

		spec := &dalec.Spec{
			Name:        fmt.Sprintf("%s-dependencies", pkgName),
			Description: "Wrapper for installing dependencies for " + pkgName,
			Version:     "1.0",
			License:     "Apache 2.0",
			Revision:    "1",
			Dependencies: &dalec.PackageDependencies{
				Runtime: deps,
			},
		}

		rpmSpec, err := rpm.ToSpecLLB(spec, in, targetKey, "", opts...)
		if err != nil {
			return dalec.ErrorState(in, err)
		}

		specPath := filepath.Join("SPECS", spec.Name, spec.Name+".spec")
		builder := in.With(dalec.SetBuildNetworkMode(spec))
		cacheInfo := rpm.CacheInfo{TargetKey: targetKey, Caches: spec.Build.Caches}
		rpmDir := rpm.Build(rpmSpec, builder, specPath, cacheInfo, opts...)

		const rpmMountDir = "/tmp/internal/dalec/deps/install/rpms"

		// Sign the wrapper RPM so it can pass GPG checks
		pg := dalec.ProgressGroup("Sign wrapper dependency package")

		// Generate GPG key and export it
		gpgKeyScriptDt := `#!/usr/bin/env sh
set -exu

gpg --batch --gen-key <<EOF
Key-Type: RSA
Key-Length: 2048
Name-Real: Dalec Dependencies
Name-Email: dalec-deps@local
Expire-Date: 0
%no-protection
%commit
EOF
`
		gpgKeyScript := llb.Scratch().File(
			llb.Mkfile("create-gpg.sh", 0o700, []byte(gpgKeyScriptDt)),
			pg,
			dalec.WithConstraints(opts...),
		)
		const scriptPath = "/tmp/dalec/internal/dnf/create-gpg.sh"
		inWithGPG := in.
			Run(
				llb.Args([]string{scriptPath}),
				llb.AddMount(scriptPath, gpgKeyScript, llb.SourcePath("create-gpg.sh"), llb.Readonly),
			)

		gpgKey := inWithGPG.Run(
			dalec.ShArgs(`gpg --armor --export dalec-deps@local > /tmp/out/deps.asc`),
		).AddMount("/tmp/out", llb.Scratch())

		const signPkgScriptDt = `
if ! command -v rpmsign >/dev/null 2>&1; then
	echo "rpmsign not found, cannot sign packages" >&2
	echo "Package installation may fail if GPG checks are enabled" >&2
	exit 0
fi

set -exu
ID=$(gpg --list-keys --keyid-format LONG | grep -B 2 'dalec-deps@local' | grep 'pub' | awk '{print $2}' | cut -d'/' -f2)
if [ -z "$ID" ]; then
	echo "Failed to find GPG key ID" >&2
	exit 42
fi
echo "%_gpg_name $ID" > ~/.rpmmacros
find /tmp/out -name "*.rpm" -exec rpmsign --addsign {} \;
`
		signPkgScript := llb.Scratch().File(
			llb.Mkfile("sign-packages.sh", 0o700, []byte(signPkgScriptDt)),
			pg,
			dalec.WithConstraints(opts...),
		)
		const signPkgScriptPath = "/tmp/dalec/internal/dnf/sign-packages.sh"

		rpmDir = inWithGPG.Run(
			dalec.ShArgs(signPkgScriptPath),
			llb.AddMount(signPkgScriptPath, signPkgScript, llb.SourcePath("sign-packages.sh"), llb.Readonly),
			pg,
		).AddMount("/tmp/out", rpmDir)

		repoMounts, keyPaths := cfg.RepoMounts(repos, sOpt, opts...)
		keyMountPath := filepath.Join(cfg.RepoPlatformConfig.GPGKeyRoot, "_internal_dalec_deps.asc")
		keyPaths = append(keyPaths, keyMountPath)

		installOpts := []DnfInstallOpt{
			DnfWithMounts(llb.AddMount(rpmMountDir, rpmDir, llb.SourcePath("/RPMS"), llb.Readonly)),
			DnfWithMounts(llb.AddMount(keyMountPath, gpgKey, llb.SourcePath("/deps.asc"), llb.Readonly)),
			DnfWithMounts(repoMounts),
			DnfImportKeys(keyPaths),
		}

		install := cfg.Install([]string{filepath.Join(rpmMountDir, "*/*.rpm")}, installOpts...)
		opts = append(opts, deps.GetSourceLocation(in))
		return in.Run(install, dalec.WithConstraints(opts...)).Root()
	}
}

func (cfg *Config) DownloadDeps(worker llb.State, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, constraints dalec.PackageDependencyList, opts ...llb.ConstraintsOpt) llb.State {
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
