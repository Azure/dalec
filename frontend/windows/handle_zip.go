package windows

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"golang.org/x/exp/maps"
	"golang.org/x/exp/slices"
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

		bin, err := buildBinaries(ctx, spec, worker, client, sOpt, targetKey)
		if err != nil {
			return nil, nil, fmt.Errorf("unable to build binaries: %w", err)
		}

		st := getZipLLB(worker, spec.Name, bin)

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

func installBuildDeps(deps []string) llb.StateOption {
	return func(s llb.State) llb.State {
		if len(deps) == 0 {
			return s
		}

		sorted := slices.Clone(deps)
		slices.Sort(sorted)

		return s.Run(
			dalec.ShArgs("apt-get update && apt-get install -y "+strings.Join(sorted, " ")),
			dalec.WithMountedAptCache(aptCachePrefix),
		).Root()
	}
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

func buildBinaries(ctx context.Context, spec *dalec.Spec, worker llb.State, client gwclient.Client, sOpt dalec.SourceOpts, targetKey string) (llb.State, error) {
	deps := dalec.SortMapKeys(spec.GetBuildDeps(targetKey))

	// note: we do not yet support pinning build dependencies for windows workers
	worker = worker.With(installBuildDeps(deps))

	sources, err := specToSourcesLLB(worker, spec, sOpt)
	if err != nil {
		return llb.Scratch(), errors.Wrap(err, "could not generate sources")
	}

	patched := dalec.PatchSources(worker, spec, sources)
	buildScript := createBuildScript(spec)
	binaries := maps.Keys(spec.Artifacts.Binaries)
	script := generateInvocationScript(binaries)

	st := worker.Run(
		dalec.ShArgs(script.String()),
		llb.Dir("/build"),
		withSourcesMounted("/build", patched, spec.Sources),
		llb.AddMount("/tmp/scripts", buildScript),
		llb.Network(llb.NetModeNone),
	).AddMount(outputDir, llb.Scratch())

	return frontend.MaybeSign(ctx, client, st, spec, targetKey, sOpt)
}

func getZipLLB(worker llb.State, name string, artifacts llb.State) llb.State {
	outName := filepath.Join(outputDir, name+".zip")
	zipped := worker.Run(
		dalec.ShArgs("zip "+outName+" *"),
		llb.Dir("/tmp/artifacts"),
		llb.AddMount("/tmp/artifacts", artifacts),
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

	return llb.Image(workerImgRef, llb.WithMetaResolver(sOpt.Resolver), dalec.WithConstraints(opts...)).
		Run(
			dalec.ShArgs("apt-get update && apt-get install -y build-essential binutils-mingw-w64 g++-mingw-w64-x86-64 gcc git make pkg-config quilt zip"),
			dalec.WithMountedAptCache(aptCachePrefix),
		).Root(), nil
}

func createBuildScript(spec *dalec.Spec) llb.State {
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
		File(llb.Mkfile(buildScriptName, 0o770, buf.Bytes()))
}
