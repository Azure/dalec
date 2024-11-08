package azlinux

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Azure/dalec"
	"github.com/moby/buildkit/client/llb"
)

type installConfig struct {
	// Tells the installer to create the distroless rpm manifest.
	manifest bool
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

type installOpt func(*installConfig)

func noGPGCheck(cfg *installConfig) {
	cfg.noGPGCheck = true
}

// see comment in tdnfInstall for why this additional option is needed
func importKeys(keys []string) installOpt {
	return func(cfg *installConfig) {
		cfg.keys = append(cfg.keys, keys...)
	}
}

func withMounts(opts ...llb.RunOption) installOpt {
	return func(cfg *installConfig) {
		cfg.mounts = append(cfg.mounts, opts...)
	}
}

func atRoot(root string) installOpt {
	return func(cfg *installConfig) {
		cfg.root = root
	}
}

func installWithConstraints(opts []llb.ConstraintsOpt) installOpt {
	return func(cfg *installConfig) {
		cfg.constraints = opts
	}
}

func tdnfInstallFlags(cfg *installConfig) string {
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

func setInstallOptions(cfg *installConfig, opts []installOpt) {
	for _, o := range opts {
		o(cfg)
	}
}

func manifestScript(workPath string, opts ...llb.ConstraintsOpt) llb.State {
	mfstDir := filepath.Join(workPath, "var/lib/rpmmanifest")
	mfst1 := filepath.Join(mfstDir, "container-manifest-1")
	mfst2 := filepath.Join(mfstDir, "container-manifest-2")
	rpmdbDir := filepath.Join(workPath, "var/lib/rpm")

	chrootedPaths := []string{
		filepath.Join(workPath, "/usr/local/bin"),
		filepath.Join(workPath, "/usr/local/sbin"),
		filepath.Join(workPath, "/usr/bin"),
		filepath.Join(workPath, "/usr/sbin"),
		filepath.Join(workPath, "/bin"),
		filepath.Join(workPath, "/sbin"),
	}
	chrootedPathEnv := strings.Join(chrootedPaths, ":")

	return llb.Scratch().File(llb.Mkfile("manifest.sh", 0o700, []byte(`
#!/usr/bin/env sh

# If the rpm command is in the rootfs then we don't need to do anything
# If not then this is a distroless image and we need to generate manifests of the installed rpms and cleanup the rpmdb.

PATH="`+chrootedPathEnv+`" command -v rpm && exit 0

set -e

mkdir -p `+mfstDir+`

rpm --dbpath=`+rpmdbDir+` -qa > `+mfst1+`
rpm --dbpath=`+rpmdbDir+` -qa --qf "%{NAME}\t%{VERSION}-%{RELEASE}\t%{INSTALLTIME}\t%{BUILDTIME}\t%{VENDOR}\t(none)\t%{SIZE}\t%{ARCH}\t%{EPOCHNUM}\t%{SOURCERPM}\n" > `+mfst2+`
rm -rf `+rpmdbDir+`
`)), opts...)
}

func importGPGScript(keyPaths []string) string {
	// all keys that are included should be mounted under this path
	keyRoot := "/etc/pki/rpm-gpg"

	var importScript string = "#!/usr/bin/env sh\nset -eux\n"
	for _, keyPath := range keyPaths {
		keyName := filepath.Base(keyPath)
		importScript += fmt.Sprintf("gpg --import %s\n", filepath.Join(keyRoot, keyName))
	}

	return importScript
}

const manifestSh = "manifest.sh"

func tdnfInstall(cfg *installConfig, relVer string, pkgs []string) llb.RunOption {
	cmdFlags := tdnfInstallFlags(cfg)
	// tdnf makecache is needed to ensure that the package metadata is up to date if extra repo
	// config files have been mounted
	cmdArgs := fmt.Sprintf("set -ex; tdnf makecache; tdnf install -y --refresh --releasever=%s %s %s", relVer, cmdFlags, strings.Join(pkgs, " "))

	var runOpts []llb.RunOption

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
