package deb

import (
	"path/filepath"

	"github.com/Azure/dalec"
	"github.com/moby/buildkit/client/llb"
)

// AptInstall returns an [llb.RunOption] that uses apt to install the provided
// packages.
//
// This returns an [llb.RunOption] but it does create some things internally,
// so any [llb.ConstraintsOpt] you want to pass in should come before this option
// in a call to [llb.State.Run].
func AptInstall(packages ...string) llb.RunOption {
	return dalec.RunOptFunc(func(ei *llb.ExecInfo) {
		const installScript = `#!/usr/bin/env sh
set -ex

# Make sure any cached data from local repos is purged since this should not
# be shared between builds.
rm -f /var/lib/apt/lists/_*
apt autoclean -y

apt update
apt install -y $@
`
		script := llb.Scratch().File(
			llb.Mkfile("install.sh", 0o755, []byte(installScript)),
			dalec.WithConstraint(&ei.Constraints),
		)

		p := "/tmp/dalec/internal/deb/install.sh"
		llb.AddMount(p, script, llb.SourcePath("install.sh")).SetRunOption(ei)
		llb.AddEnv("DEBIAN_FRONTEND", "noninteractive").SetRunOption(ei)
		llb.Args(append([]string{p}, packages...)).SetRunOption(ei)
	})
}

// InstallLocalPkg installs all deb packages found in the root of the provided [llb.State]
//
// In some cases, with strict version constraints in the package's dependencies,
// this will use `aptitude` to help resolve those dependencies since apt is
// currently unable to handle strict constraints.
//
// This returns an [llb.RunOption] but it does create some things internally,
// so any [llb.ConstraintsOpt] you want to pass in should come before this option
// in a call to [llb.State.Run].
func InstallLocalPkg(pkg llb.State) llb.RunOption {
	return dalec.RunOptFunc(func(ei *llb.ExecInfo) {
		// The apt solver always tries to select the latest package version even when constraints specify that an older version should be installed and that older version is available in a repo.
		// This leads the solver to simply refuse to install our target package if the latest version of ANY dependency package is incompatible with the constraints.
		// To work around this we first install the .deb for the package with dpkg, specifically ignoring any dependencies so that we can avoid the constraints issue.
		// We then use aptitude to fix the (possibly broken) install of the package, and we pass the aptitude solver a hint to REJECT any solution that involves uninstalling the package.
		// This forces aptitude to find a solution that will respect the constraints even if the solution involves pinning dependency packages to older versions.
		const installScript = `#!/usr/bin/env sh
set -ex

# Make sure any cached data from local repos is purged since this should not
# be shared between builds.
rm -f /var/lib/apt/lists/_*
apt autoclean -y

apt update

if ! command -v apititude > /dev/null; then
	needs_cleanup=1
	apt install -y aptitude
fi

cleanup() {
	if [ "${needs_cleanup}" = "1" ]; then
		apt remove -y aptitude
	fi
}

trap cleanup EXIT

dpkg -i --force-depends ${1}

pkg_name="$(dpkg-deb -f ${1} | grep 'Package:' | awk -F': ' '{ print $2 }')"
aptitude install -y -f -o "Aptitude::ProblemResolver::Hints::=reject ${pkg_name} :UNINST"
`

		script := llb.Scratch().File(
			llb.Mkfile("install.sh", 0o755, []byte(installScript)),
			dalec.WithConstraint(&ei.Constraints),
		)

		p := "/tmp/dalec/internal/deb/install-with-constraints.sh"
		debPath := "/tmp/dalec/internal/debs"

		llb.AddMount(p, script, llb.SourcePath("install.sh")).SetRunOption(ei)
		llb.AddMount(debPath, pkg, llb.Readonly).SetRunOption(ei)
		llb.AddEnv("DEBIAN_FRONTEND", "noninteractive").SetRunOption(ei)

		args := []string{p, filepath.Join(debPath, "*.deb")}
		llb.Args(args).SetRunOption(ei)
	})
}
