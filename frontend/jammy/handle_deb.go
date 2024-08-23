package jammy

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/frontend/deb"
	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

const JammyWorkerContextName = "dalec-jammy-worker"

func handleDeb(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		sOpt, err := frontend.SourceOptFromClient(ctx, client)
		if err != nil {
			return nil, nil, err
		}

		st, err := buildDeb(ctx, client, spec, sOpt, targetKey, dalec.ProgressGroup("Building Jammy deb package: "+spec.Name))
		if err != nil {
			return nil, nil, err
		}

		def, err := st.Marshal(ctx)
		if err != nil {
			return nil, nil, err
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
		if platform == nil {
			p := platforms.DefaultSpec()
			platform = &p
		}
		return ref, &dalec.DockerImageSpec{Image: ocispecs.Image{Platform: *platform}}, nil
	})
}

func installPackages(ls ...string) llb.RunOption {
	return dalec.RunOptFunc(func(ei *llb.ExecInfo) {
		// This only runs apt-get update if the pkgcache is older than 10 minutes.
		dalec.ShArgs(`set -ex; apt update; apt install -y ` + strings.Join(ls, " ")).SetRunOption(ei)
		dalec.WithMountedAptCache(AptCachePrefix).SetRunOption(ei)
	})
}

func buildDeb(ctx context.Context, client gwclient.Client, spec *dalec.Spec, sOpt dalec.SourceOpts, targetKey string, opts ...llb.ConstraintsOpt) (llb.State, error) {
	worker, err := workerBase(sOpt, opts...)
	if err != nil {
		return llb.Scratch(), err
	}

	installBuildDeps, err := buildDepends(worker, sOpt, spec, targetKey, opts...)
	if err != nil {
		return llb.Scratch(), errors.Wrap(err, "error creating deb for build dependencies")
	}

	worker = worker.With(installBuildDeps)
	st, err := deb.BuildDeb(worker, spec, sOpt, targetKey, opts...)
	if err != nil {
		return llb.Scratch(), err
	}

	signed, err := frontend.MaybeSign(ctx, client, st, spec, targetKey, sOpt)
	if err != nil {
		return llb.Scratch(), err
	}
	return signed, nil
}

func workerBase(sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, error) {
	base, err := sOpt.GetContext(jammyRef, dalec.WithConstraints(opts...))
	if err != nil {
		return llb.Scratch(), err
	}
	if base != nil {
		return *base, nil
	}

	base, err = sOpt.GetContext(JammyWorkerContextName, dalec.WithConstraints(opts...))
	if err != nil {
		return llb.Scratch(), err
	}

	if base != nil {
		return *base, nil
	}

	return llb.Image(jammyRef, llb.WithMetaResolver(sOpt.Resolver)).With(basePackages(opts...)), nil
}

func basePackages(opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		opts = append(opts, dalec.ProgressGroup("Install base packages"))
		return in.Run(
			installPackages("dpkg-dev", "devscripts", "equivs", "fakeroot", "dh-make", "build-essential", "dh-apparmor", "dh-make", "dh-exec", "debhelper-compat="+deb.DebHelperCompat),
			dalec.WithConstraints(opts...),
		).Root()
	}
}

func buildDepends(worker llb.State, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, opts ...llb.ConstraintsOpt) (llb.StateOption, error) {
	deps := spec.Dependencies
	if t, ok := spec.Targets[targetKey]; ok {
		if t.Dependencies != nil {
			deps = t.Dependencies
		}
	}

	var buildDeps map[string]dalec.PackageConstraints
	if deps != nil {
		buildDeps = deps.Build
	}

	if len(buildDeps) == 0 {
		return func(in llb.State) llb.State {
			return in
		}, nil
	}

	const getInstallCandidateScript = `#!/usr/bin/env bash

set -euxo pipefail

pkg="${1}"
shift

# Check if the arches for this package constraint match the current architecture
# If not there's nothing to install
# If no arches are set then assume it is a matching arch
if [ -n "${DEB_ARCHES}" ]; then
	arches=(${DEB_ARCHES})
	if [[ ! ${arches[@]} =~ "$(dpkg --print-architecture)" ]]; then
		exit 0
	fi
fi

apt update

versions=($(apt-cache madison "${pkg}" | awk -F'|' '{ print $2 }' | tr -d '[ ]'))

constraints=("${@}")

fulfills_constraints() {
	for i in "${constraints[@]}"; do
	 	# Note: $i is the full constraint value, e.g. ">= 1.3.0"
		# Hence why it is not quoted here.
		# dpkg --compare-versions takes 3 arguments: <ver1> <comparison op> <ver2>
		dpkg --compare-versions "${1}" ${i}
	done
}

for v in ${versions[@]}; do
	if fulfills_constraints "$v"; then
		apt install -y "${pkg}=${v}"
		exit 0
	fi
done

join_constraints() {
  local IFS=" && "
  echo "${constraints[*]}"
}


echo No candidates match constraints for package "${pkg}" with constraints $(join_constraints) >&2
# Note: This is not going to exit with non-zero because this is a best-effort to
# get any deps installed that may give the apt solver trouble.
`

	script := llb.Scratch().File(
		llb.Mkfile("deps.sh", 0o774, []byte(getInstallCandidateScript)),
		opts...,
	)

	scriptDir := "/tmp/internal/dalec/build"

	sorted := dalec.SortMapKeys(buildDeps)

	depsSpec := &dalec.Spec{
		Name:     spec.Name + "-deps",
		Packager: "Dalec",
		Version:  spec.Version,
		Revision: spec.Revision,
		Dependencies: &dalec.PackageDependencies{
			Runtime: buildDeps,
		},
		Description: "Build dependencies for " + spec.Name,
	}

	pg := dalec.ProgressGroup("Install build dependencies")
	opts = append(opts, pg)
	pkg, err := deb.BuildDeb(worker, depsSpec, sOpt, targetKey, append(opts, dalec.ProgressGroup("Create intermediate deb for build dependnencies"))...)
	if err != nil {
		return nil, errors.Wrap(err, "error creating intermediate package for installing build dependencies")
	}

	return func(in llb.State) llb.State {
		for _, pkg := range sorted {
			c := buildDeps[pkg]

			if len(c.Version) == 0 {
				continue
			}

			// APT's dependency resolution *always* tries to install the latest
			// version of a package's dependencies regardless of the provided
			// package constraints.
			// If the latest package does not fulfill the constraints then it errors out.
			// But... we really want to be able to build stuff with the provided
			// constraints.
			// Instead of using APT's normal solver here we'll do our own version
			// resolution.
			//
			// Because Ubuntu typically removes older versions of packages from the
			// repos when a newer one becomes available, this may not work out.
			// Generally I would recommend against using `=`, `<`, or `<=` in version
			// constraints.
			opts := append(opts, dalec.ProgressGroup("Determine version matching constraints for package "+pkg))
			args := []string{filepath.Join(scriptDir, "deps.sh"), pkg}
			args = append(args, c.Version...)

			in = in.Run(
				llb.Args(args),
				llb.AddEnv("DEB_ARCHES", strings.Join(c.Arch, " ")),
				dalec.WithConstraints(opts...),
				dalec.WithMountedAptCache(AptCachePrefix),
				llb.AddMount(scriptDir, script, llb.Readonly),
			).Root()
		}

		const (
			debPath = "/tmp/dalec/internal/build/deps"
		)
		return in.Run(
			installPackages(debPath+"/*.deb"),
			llb.AddMount(debPath, pkg, llb.Readonly),
			dalec.WithConstraints(opts...),
		).Root()
	}, nil
}
