package distro

import (
	"context"
	"path/filepath"
	"strconv"

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
func InstallLocalPkg(pkg llb.State, upgrade bool, opts ...llb.ConstraintsOpt) llb.RunOption {
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

if [ "${DALEC_UPGRADE}" = "true" ]; then
	apt dist-upgrade -y
fi

if apt install -y ${1}; then
	exit 0
fi

if ! command -v aptitude > /dev/null; then
	needs_cleanup=1
	apt install -y aptitude
fi

cleanup() {
	exit_code=$?
	if [ "${needs_cleanup}" = "1" ]; then
		apt remove -y aptitude
	fi
	exit $exit_code
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
		llb.AddEnv("DALEC_UPGRADE", strconv.FormatBool(upgrade)).SetRunOption(ei)

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

			customRepos := d.RepoMounts(repos, sOpt, opts...)

			return in.Run(
				dalec.WithConstraints(opts...),
				customRepos,
				InstallLocalPkg(pkg, false, opts...),
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
		repos := dalec.GetExtraRepos(d.ExtraRepos, "test")
		repos = append(repos, spec.GetTestRepos(targetKey)...)

		withRepos := d.RepoMounts(repos, sOpt, opts...)

		opts = append(opts, dalec.ProgressGroup("Install test dependencies"))
		return in.Run(
			dalec.WithConstraints(opts...),
			AptInstall(deps, opts...),
			withRepos,
			dalec.WithMountedAptCache(d.AptCachePrefix),
		).Root()
	}
}

func (d *Config) DownloadDeps(worker llb.State, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, opts ...llb.ConstraintsOpt) llb.State {
	opts = append(opts, dalec.ProgressGroup("Downloading dependencies"))

	deps := spec.GetPackageDeps(targetKey)
	if deps == nil {
		return llb.Scratch()
	}

	constraints := deps.Runtime
	if constraints == nil {
		return llb.Scratch()
	}

	scriptPath := "/tmp/dalec/internal/deb/download.sh"
	const scriptSrc = `#!/usr/bin/env bash
set -euxo pipefail
cd /output

# Make sure any cached data from local repos is purged since this should not
# be shared between builds.
rm -f /var/lib/apt/lists/_*
apt autoclean -y
apt update

# Use APT to resolve the constraints and download just the requested packages
# without the sub-dependencies. Ideally, we would resolve all the constraints
# together and match the packages by name, but the resolved name is often
# different. We therefore have to resolve each constraint in turn and assume
# that the last Inst line corresponds to the requested package. This should be
# the case when recommends and suggests are omitted.
for CONSTRAINT; do
	apt satisfy -y -s --no-install-recommends --no-install-suggests "${CONSTRAINT}" |
		tac |
		sed -n -r 's/^Inst ([^ ]+) \(([^ ]+).*/\1=\2/p;T;q' |
		xargs -t apt download
done
`

	scriptFile := llb.Scratch().File(
		llb.Mkfile("download.sh", 0o755, []byte(scriptSrc)),
		dalec.WithConstraints(opts...),
	)

	return worker.Run(
		llb.Args(append([]string{scriptPath}, deb.AppendConstraints(constraints)...)),
		llb.AddMount(scriptPath, scriptFile, llb.SourcePath("download.sh"), llb.Readonly),
		llb.AddMount("/var/lib/dpkg", llb.Scratch(), llb.Tmpfs()),
		llb.AddEnv("DEBIAN_FRONTEND", "noninteractive"),
		dalec.WithMountedAptCache(d.AptCachePrefix),
		dalec.WithConstraints(opts...),
	).AddMount("/output", llb.Scratch())
}
