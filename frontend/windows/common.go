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
	"github.com/pkg/errors"
	"golang.org/x/exp/slices"
)

const (
	workerImgRef    = "mcr.microsoft.com/mirror/docker/library/ubuntu:jammy"
	outputDir       = "/tmp/output"
	buildScriptName = "_build.sh"
	aptCachePrefix  = "jammy-windowscross"
)

const gomodsName = "__gomods"

func specToSourcesLLB(worker llb.State, spec *dalec.Spec, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) (map[string]llb.State, error) {
	out := make(map[string]llb.State, len(spec.Sources))
	for k, src := range spec.Sources {
		displayRef, err := src.GetDisplayRef()
		if err != nil {
			return nil, err
		}

		pg := dalec.ProgressGroup("Add spec source: " + k + " " + displayRef)
		st, err := src.AsState(k, sOpt, append(opts, pg)...)
		if err != nil {
			return nil, errors.Wrapf(err, "error creating source state for %q", k)
		}

		out[k] = st
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
			shArgs("apt-get update && apt-get install -y "+strings.Join(sorted, " ")),
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

		// In cases where we have a generated soruce (e.g. gomods) we don't have a [dalec.Source] in the `sources` map.
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
	worker = worker.With(installBuildDeps(spec.GetBuildDeps(targetKey)))

	sources, err := specToSourcesLLB(worker, spec, sOpt)
	if err != nil {
		return llb.Scratch(), errors.Wrap(err, "could not generate sources")
	}

	patched := dalec.PatchSources(worker, spec, sources)
	buildScript := createBuildScript(spec)
	script := generateInvocationScript(spec.Artifacts.Binaries)

	pg := dalec.ProgressGroup("Build binaries")

	st := worker.Run(
		shArgs(script.String()),
		llb.Dir("/build"),
		withSourcesMounted("/build", patched, spec.Sources),
		llb.AddMount("/tmp/scripts", buildScript),
		llb.Network(llb.NetModeNone),
		pg,
	).AddMount(outputDir, llb.Scratch())

	if signer, ok := spec.GetSigner(targetKey); ok {
		signed, err := frontend.ForwardToSigner(ctx, client, signer, st)
		if err != nil {
			return llb.Scratch(), err
		}

		st = signed
	}

	return st, nil
}

func generateInvocationScript(binaryArtifacts map[string]dalec.ArtifactConfig) *strings.Builder {
	script := &strings.Builder{}
	fmt.Fprintln(script, "#!/usr/bin/env sh")
	fmt.Fprintln(script, "set -ex")
	fmt.Fprintf(script, "/tmp/scripts/%s\n", buildScriptName)
	for path, bin := range binaryArtifacts {
		fmt.Fprintf(script, "mv '%s' '%s'\n", path, filepath.Join(outputDir, bin.ResolveName(path)))
	}
	return script
}

func workerImg(sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) llb.State {
	// TODO: support named context override... also this should possibly be its own image, maybe?
	return llb.Image(workerImgRef, llb.WithMetaResolver(sOpt.Resolver), dalec.WithConstraints(opts...)).
		Run(
			shArgs("apt-get update && apt-get install -y build-essential binutils-mingw-w64 g++-mingw-w64-x86-64 gcc git make pkg-config quilt zip"),
			dalec.WithMountedAptCache(aptCachePrefix),
		).Root()
}

func shArgs(cmd string) llb.RunOption {
	return llb.Args([]string{"sh", "-c", cmd})
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
