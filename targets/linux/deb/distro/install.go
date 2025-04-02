package distro

import (
	"context"
	"path/filepath"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/packaging/linux/deb"
	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"
)

// AptInstall returns an [llb.RunOption] that uses apt to install the provided
// packages.
//
// This returns an [llb.RunOption] but it does create some things internally,
// This is what the constraints opts are used for.
// The constraints are applied after any constraint set on the [llb.ExecInfo]
func AptInstall(packages []string, opts ...llb.ConstraintsOpt) llb.RunOption {
	return dalec.RunOptFunc(func(ei *llb.ExecInfo) {
		const installScript = `#!/usr/bin/env sh
set -ex

# Make sure any cached data from local repos is purged since this should not
# be shared between builds.
rm -f /var/lib/apt/lists/_*
apt autoclean -y

# Remove any previously failed attempts to get repo data
rm -rf /var/lib/apt/lists/partial/*

apt update
apt install -y "$@"
`
		script := llb.Scratch().File(
			llb.Mkfile("install.sh", 0o755, []byte(installScript)),
			dalec.WithConstraint(&ei.Constraints),
			dalec.WithConstraints(opts...),
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
// This is what the constraints opts are used for.
// The constraints are applied after any constraint set on the [llb.ExecInfo]
func InstallLocalPkg(pkg llb.State, opts ...llb.ConstraintsOpt) llb.RunOption {
	return dalec.RunOptFunc(func(ei *llb.ExecInfo) {
		// The apt solver always tries to select the latest package version even
		// when constraints specify that an older version should be installed and
		// that older version is available in a repo. This leads the solver to
		// simply refuse to install our target package if the latest version of ANY
		// dependency package is incompatible with the constraints. To work around
		// this we first install the .deb for the package with dpkg, specifically
		// ignoring any dependencies so that we can avoid the constraints issue.
		// We then use aptitude to fix the (possibly broken) install of the
		// package, and we pass the aptitude solver a hint to REJECT any solution
		// that involves uninstalling the package. This forces aptitude to find a
		// solution that will respect the constraints even if the solution involves
		// pinning dependency packages to older versions.
		const installScript = `#!/usr/bin/env sh
set -ex

# Make sure any cached data from local repos is purged since this should not
# be shared between builds.
rm -f /var/lib/apt/lists/_*
apt autoclean -y

# Remove any previously failed attempts to get repo data
rm -rf /var/lib/apt/lists/partial/*
apt update

if ! command -v aptitude > /dev/null; then
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
			dalec.WithConstraints(opts...),
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

func (d *Config) InstallBuildDeps(sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		buildDeps := spec.GetBuildDeps(targetKey)
		if len(buildDeps) == 0 {
			return in
		}

		depsSpec := &dalec.Spec{
			Name:     spec.Name + "-build-deps",
			Packager: "Dalec",
			Version:  spec.Version,
			Revision: spec.Revision,
			Dependencies: &dalec.PackageDependencies{
				Runtime: buildDeps,
			},
			Description: "Build dependencies for " + spec.Name,
		}

		return in.Async(func(ctx context.Context, in llb.State, c *llb.Constraints) (llb.State, error) {
			opts := append(opts, dalec.ProgressGroup("Install build dependencies"))
			opts = append([]llb.ConstraintsOpt{dalec.WithConstraint(c)}, opts...)

			debRoot, err := deb.Debroot(ctx, sOpt, depsSpec, in, llb.Scratch(), targetKey, "", d.VersionID, deb.SourcePkgConfig{}, opts...)
			if err != nil {
				return in, err
			}

			pkg, err := deb.BuildDebBinaryOnly(in, depsSpec, debRoot, "", opts...)
			if err != nil {
				return in, errors.Wrap(err, "error creating intermediate package for installing build dependencies")
			}

			repos := dalec.GetExtraRepos(d.ExtraRepos, "build")
			repos = append(repos, spec.GetBuildRepos(targetKey)...)

			customRepos, err := d.RepoMounts(repos, sOpt, opts...)
			if err != nil {
				return in, err
			}

			return in.Run(
				dalec.WithConstraints(opts...),
				customRepos,
				InstallLocalPkg(pkg, opts...),
				dalec.WithMountedAptCache(d.AptCachePrefix),
			).Root(), nil
		})
	}
}

func (d *Config) InstallTestDeps(sOpt dalec.SourceOpts, targetKey string, spec *dalec.Spec, opts ...llb.ConstraintsOpt) llb.StateOption {
	deps := spec.GetTestDeps(targetKey)
	if len(deps) == 0 {
		return func(s llb.State) llb.State { return s }
	}

	return func(in llb.State) llb.State {
		return in.Async(func(ctx context.Context, in llb.State, c *llb.Constraints) (llb.State, error) {
			repos := dalec.GetExtraRepos(d.ExtraRepos, "test")
			repos = append(repos, spec.GetTestRepos(targetKey)...)

			withRepos, err := d.RepoMounts(repos, sOpt, opts...)
			if err != nil {
				return in, err
			}

			opts = append(opts, dalec.ProgressGroup("Install test dependencies"))
			return in.Run(
				dalec.WithConstraints(opts...),
				AptInstall(deps, opts...),
				withRepos,
				dalec.WithMountedAptCache(d.AptCachePrefix),
			).Root(), nil
		})
	}
}
