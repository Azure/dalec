package windows

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/frontend/deb"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"golang.org/x/exp/maps"
)

const (
	workerImgRef                  = "mcr.microsoft.com/mirror/docker/library/ubuntu:jammy"
	WindowscrossWorkerContextName = "dalec-windowscross-worker"
	outputDir                     = "/tmp/output"
	buildScriptName               = "_build.sh"
	aptCachePrefix                = "jammy-windowscross"
)

func handleZip(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		sOpt, err := frontend.SourceOptFromClient(ctx, client)
		if err != nil {
			return nil, nil, err
		}

		pg := dalec.ProgressGroup("Build windows container: " + spec.Name)
		worker, err := workerImg(sOpt, pg)
		if err != nil {
			return nil, nil, err
		}

		bin, err := buildBinaries(ctx, spec, worker, client, sOpt, targetKey, pg)
		if err != nil {
			return nil, nil, fmt.Errorf("unable to build binaries: %w", err)
		}

		st := getZipLLB(worker, spec.Name, bin, pg)

		def, err := st.Marshal(ctx)
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
		return ref, &dalec.DockerImageSpec{}, err
	})
}

const gomodsName = "__gomods"

func specToSourcesLLB(worker llb.State, spec *dalec.Spec, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) (map[string]llb.State, error) {
	out, err := dalec.Sources(spec, sOpt, opts...)
	if err != nil {
		return nil, errors.Wrap(err, "error preparign spec sources")
	}

	opts = append(opts, dalec.ProgressGroup("Add gomod sources"))
	st, err := spec.GomodDeps(sOpt, worker, opts...)
	if err != nil {
		return nil, errors.Wrap(err, "error adding gomod sources")
	}

	if st != nil {
		out[gomodsName] = *st
	}

	return out, nil
}

func installBuildDeps(sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, opts ...llb.ConstraintsOpt) llb.StateOption {

	return func(in llb.State) llb.State {
		deps := spec.GetBuildDeps(targetKey)
		if len(deps) == 0 {
			return in
		}

		return in.Async(func(ctx context.Context, in llb.State, c *llb.Constraints) (llb.State, error) {
			depsSpec := &dalec.Spec{
				Name:     spec.Name + "-deps",
				Packager: "Dalec",
				Version:  spec.Version,
				Revision: spec.Revision,
				Dependencies: &dalec.PackageDependencies{
					Runtime: deps,
				},
				Description: "Build dependencies for " + spec.Name,
			}

			opts = append(opts, dalec.WithConstraint(c))

			srcPkg, err := deb.SourcePackage(sOpt, in, depsSpec, targetKey, "", opts...)
			if err != nil {
				return in, err
			}

			pg := dalec.ProgressGroup("Install build dependencies")
			opts = append(opts, pg)

			pkg, err := deb.BuildDeb(in, depsSpec, srcPkg, "", append(opts, dalec.ProgressGroup("Create intermediate deb for build dependnencies"))...)
			if err != nil {
				return in, errors.Wrap(err, "error creating intermediate package for installing build dependencies")
			}

			customRepoOpts, err := customRepoMounts(spec.GetBuildRepos(targetKey), sOpt, opts...)
			if err != nil {
				return in, err
			}

			const (
				debPath = "/tmp/dalec/internal/build/deps"
			)

			return in.Run(
				installWithConstraints(debPath+"/*.deb", depsSpec.Name, opts...),
				llb.AddMount(debPath, pkg, llb.Readonly),
				customRepoOpts,
				dalec.WithConstraints(opts...),
			).Root(), nil
		})
	}
}

func installWithConstraints(pkgPath string, pkgName string, opts ...llb.ConstraintsOpt) llb.RunOption {
	return dalec.RunOptFunc(func(ei *llb.ExecInfo) {
		// The apt solver always tries to select the latest package version even when constraints specify that an older version should be installed and that older version is available in a repo.
		// This leads the solver to simply refuse to install our target package if the latest version of ANY dependency package is incompatible with the constraints.
		// To work around this we first install the .deb for the package with dpkg, specifically ignoring any dependencies so that we can avoid the constraints issue.
		// We then use aptitude to fix the (possibly broken) install of the package, and we pass the aptitude solver a hint to REJECT any solution that involves uninstalling the package.
		// This forces aptitude to find a solution that will respect the constraints even if the solution involves pinning dependency packages to older versions.
		script := llb.Scratch().File(
			llb.Mkfile("install.sh", 0o755, []byte(`#!/usr/bin/env sh
# Make sure any cached data from local repos is purged since this should not
# be shared between builds.
rm -f /var/lib/apt/lists/_*
apt autoclean -y

dpkg -i --force-depends `+pkgPath+`

apt update


set +e
aptitude install -y -f -o "Aptitude::ProblemResolver::Hints::=reject `+pkgName+` :UNINST" && exit
ls -lh /etc/apt/sources.list.d
exit 42
`),
			), opts...)

		dalec.WithMountedAptCache(aptCachePrefix).SetRunOption(ei)

		p := "/tmp/dalec/internal/deb/install-with-constraints.sh"
		llb.AddMount(p, script, llb.SourcePath("install.sh")).SetRunOption(ei)
		dalec.ShArgs(p).SetRunOption(ei)
	})
}

func withSourcesMounted(dst string, states map[string]llb.State, sources map[string]dalec.Source) llb.RunOption {
	opts := make([]llb.RunOption, 0, len(states))

	sorted := dalec.SortMapKeys(states)
	files := []llb.State{}

	for _, k := range sorted {
		state := states[k]

		// In cases where we have a generated source (e.g. gomods) we don't have a [dalec.Source] in the `sources` map.
		// So we need to check for this.
		src, ok := sources[k]

		if ok && !dalec.SourceIsDir(src) {
			files = append(files, state)
			continue
		}

		dirDst := filepath.Join(dst, k)
		opts = append(opts, llb.AddMount(dirDst, state))
	}

	ordered := make([]llb.RunOption, 1, len(opts)+1)
	ordered[0] = llb.AddMount(dst, dalec.MergeAtPath(llb.Scratch(), files, "/"))
	ordered = append(ordered, opts...)

	return dalec.WithRunOptions(ordered...)
}

func buildBinaries(ctx context.Context, spec *dalec.Spec, worker llb.State, client gwclient.Client, sOpt dalec.SourceOpts, targetKey string, opts ...llb.ConstraintsOpt) (llb.State, error) {
	worker = worker.With(installBuildDeps(sOpt, spec, targetKey))

	sources, err := specToSourcesLLB(worker, spec, sOpt, opts...)
	if err != nil {
		return llb.Scratch(), errors.Wrap(err, "could not generate sources")
	}

	patched := dalec.PatchSources(worker, spec, sources, opts...)
	buildScript := createBuildScript(spec, opts...)
	binaries := maps.Keys(spec.Artifacts.Binaries)
	script := generateInvocationScript(binaries)

	builder := worker.With(dalec.SetBuildNetworkMode(spec))
	st := builder.Run(
		dalec.ShArgs(script.String()),
		llb.Dir("/build"),
		withSourcesMounted("/build", patched, spec.Sources),
		llb.AddMount("/tmp/scripts", buildScript),
		dalec.WithConstraints(opts...),
	).AddMount(outputDir, llb.Scratch())

	return frontend.MaybeSign(ctx, client, st, spec, targetKey, sOpt)
}

func getZipLLB(worker llb.State, name string, artifacts llb.State, opts ...llb.ConstraintsOpt) llb.State {
	outName := filepath.Join(outputDir, name+".zip")
	zipped := worker.Run(
		dalec.ShArgs("zip "+outName+" *"),
		llb.Dir("/tmp/artifacts"),
		llb.AddMount("/tmp/artifacts", artifacts),
		dalec.WithConstraints(opts...),
	).AddMount(outputDir, llb.Scratch())
	return zipped
}

func generateInvocationScript(binaries []string) *strings.Builder {
	script := &strings.Builder{}
	fmt.Fprintln(script, "#!/usr/bin/env sh")
	fmt.Fprintln(script, "set -ex")
	fmt.Fprintf(script, "/tmp/scripts/%s\n", buildScriptName)
	for _, bin := range binaries {
		fmt.Fprintf(script, "mv '%s' '%s'\n", bin, outputDir)
	}
	return script
}

func workerImg(sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, error) {
	base, err := sOpt.GetContext(workerImgRef, dalec.WithConstraints(opts...))
	if err != nil {
		return llb.Scratch(), err
	}

	if base != nil {
		return *base, nil
	}

	base, err = sOpt.GetContext(WindowscrossWorkerContextName, dalec.WithConstraints(opts...))
	if err != nil {
		return llb.Scratch(), nil
	}
	if base != nil {
		return *base, nil
	}

	return llb.Image(workerImgRef, llb.WithMetaResolver(sOpt.Resolver), dalec.WithConstraints(opts...)).Run(
		dalec.ShArgs("apt-get update && apt-get install -y build-essential binutils-mingw-w64 g++-mingw-w64-x86-64 gcc git make pkg-config quilt zip aptitude dpkg-dev debhelper-compat="+deb.DebHelperCompat),
		dalec.WithMountedAptCache(aptCachePrefix),
		dalec.WithConstraints(opts...),
	).
		// This file prevents installation of things like docs in ubuntu
		// containers We don't want to exclude this because tests want to
		// check things for docs in the build container. But we also don't
		// want to remove this completely from the base worker image in the
		// frontend because we usually don't want such things in the build
		// environment. This is only needed because certain tests (which
		// are using this customized builder image) are checking for files
		// that are being excluded by this config file.
		File(llb.Rm("/etc/dpkg/dpkg.cfg.d/excludes", llb.WithAllowNotFound(true))), nil
}

func createBuildScript(spec *dalec.Spec, opts ...llb.ConstraintsOpt) llb.State {
	buf := bytes.NewBuffer(nil)

	fmt.Fprintln(buf, "#!/usr/bin/env sh")
	fmt.Fprintln(buf, "set -x")

	if spec.HasGomods() {
		fmt.Fprintln(buf, "export GOMODCACHE=\"$(pwd)/"+gomodsName+"\"")
	}

	for i, step := range spec.Build.Steps {
		fmt.Fprintln(buf, "(")

		for k, v := range step.Env {
			fmt.Fprintf(buf, "export %s=\"%s\"", k, v)
		}

		fmt.Fprintln(buf, step.Command)
		fmt.Fprintf(buf, ")")

		if i < len(spec.Build.Steps)-1 {
			fmt.Fprintln(buf, " && \\")
			continue
		}

		fmt.Fprintf(buf, "\n")
	}

	return llb.Scratch().
		File(llb.Mkfile(buildScriptName, 0o770, buf.Bytes()), opts...)
}

var jammyRepoPlatformCfg = dalec.RepoPlatformConfig{
	ConfigRoot: "/etc/apt/sources.list.d",
	GPGKeyRoot: "/usr/share/keyrings",
}

func customRepoMounts(repos []dalec.PackageRepositoryConfig, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) (llb.RunOption, error) {
	withRepos, err := dalec.WithRepoConfigs(repos, &jammyRepoPlatformCfg, sOpt, opts...)
	if err != nil {
		return nil, err
	}

	withData, err := dalec.WithRepoData(repos, sOpt, opts...)
	if err != nil {
		return nil, err
	}

	keyMounts, _, err := dalec.GetRepoKeys(repos, &jammyRepoPlatformCfg, sOpt, opts...)
	if err != nil {
		return nil, err
	}

	return dalec.WithRunOptions(withRepos, withData, keyMounts), nil
}
