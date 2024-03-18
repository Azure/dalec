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
	"github.com/moby/buildkit/exporter/containerimage/image"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"golang.org/x/exp/maps"
	"golang.org/x/exp/slices"
)

const (
	workerImgRef    = "mcr.microsoft.com/mirror/docker/library/ubuntu:jammy"
	outputDir       = "/tmp/output"
	buildScriptName = "_build.sh"
)

var (
	varCacheAptMount = llb.AddMount("/var/cache/apt", llb.Scratch(), llb.AsPersistentCacheDir("dalec-windows-var-cache-apt", llb.CacheMountLocked))
	varLibAptMount   = llb.AddMount("/var/lib/apt", llb.Scratch(), llb.AsPersistentCacheDir("dalec-windows-var-lib-apt", llb.CacheMountLocked))
)

func handleZip(ctx context.Context, client gwclient.Client, spec *dalec.Spec) (gwclient.Reference, *image.Image, error) {
	sOpt, err := frontend.SourceOptFromClient(ctx, client)
	if err != nil {
		return nil, nil, err
	}

	pg := dalec.ProgressGroup("Build windows container: " + spec.Name)
	worker := workerImg(sOpt, pg)

	bin, err := buildBinaries(spec, worker, sOpt)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to build binaries: %w", err)
	}

	st := getZipLLB(worker, spec.Name, bin)
	if err != nil {
		return nil, nil, err
	}

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
	// Do not return a nil image, it may cause a panic
	return ref, &image.Image{}, err
}

func specToSourcesLLB(spec *dalec.Spec, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) (map[string]llb.State, error) {
	out := make(map[string]llb.State, len(spec.Sources))
	for k, src := range spec.Sources {
		isDir := dalec.SourceIsDir(src)

		s := ""
		switch {
		case src.DockerImage != nil:
			s = src.DockerImage.Ref
		case src.Git != nil:
			s = src.Git.URL
		case src.HTTP != nil:
			s = src.HTTP.URL
		case src.Context != nil:
			s = src.Context.Name
		case src.Build != nil:
			s = fmt.Sprintf("%v", src.Build.Source)
		case src.Inline != nil:
			s = "inline"
		default:
			return nil, fmt.Errorf("no non-nil source provided")
		}

		pg := dalec.ProgressGroup("Add spec source: " + k + " " + s)
		st, err := src.AsState(k, sOpt, append(opts, pg)...)
		if err != nil {
			return nil, err
		}

		if isDir {
			st = llb.Scratch().File(llb.Copy(st, "/", filepath.Join("/", k)))
		}

		out[k] = st
	}

	return out, nil
}

func mapToArraySortedByKeys[T any](m map[string]T) []T {
	keys := dalec.SortMapKeys(m)
	out := make([]T, 0, len(keys))
	for _, k := range keys {
		out = append(out, m[k])
	}
	return out
}

func buildBinaries(spec *dalec.Spec, worker llb.State, sOpt dalec.SourceOpts) (llb.State, error) {
	sources, err := specToSourcesLLB(spec, sOpt)
	if err != nil {
		return llb.Scratch(), err
	}

	patchedMap := dalec.PatchSources(worker, spec, sources)

	patched := mapToArraySortedByKeys(patchedMap)

	combined := dalec.MergeAtPath(llb.Scratch(), patched, "/")
	buildScript := createBuildScript(spec)

	binaries := maps.Keys(spec.Artifacts.Binaries)
	script := generateInvocationScript(binaries)

	work := worker.
		Run(
			shArgs("apt-get update && apt-get install -y "+strings.Join(buildDeps(spec), " ")),
			varCacheAptMount,
			varLibAptMount,
		).Run(
		shArgs(script.String()),
		llb.Dir("/build"),
		llb.AddMount("/build", combined),
		llb.AddMount("/build/scripts", buildScript),
		llb.Network(llb.NetModeNone),
	)

	artifacts := work.AddMount(outputDir, llb.Scratch())
	return artifacts, nil
}

func getZipLLB(worker llb.State, name string, artifacts llb.State) llb.State {
	outName := filepath.Join(outputDir, name+".zip")
	zipped := worker.Run(
		shArgs("zip "+outName+" *"),
		llb.Dir("/tmp/artifacts"),
		llb.AddMount("/tmp/artifacts", artifacts),
	).AddMount(outputDir, llb.Scratch())
	return zipped
}

func generateInvocationScript(binaries []string) *strings.Builder {
	script := &strings.Builder{}
	fmt.Fprintln(script, "#!/usr/bin/env sh")
	fmt.Fprintln(script, "set -ex")
	fmt.Fprintf(script, "./scripts/%s\n", buildScriptName)
	for _, bin := range binaries {
		fmt.Fprintf(script, "mv '%s' '%s'\n", bin, outputDir)
	}
	return script
}

func buildDeps(spec *dalec.Spec) []string {
	deps := dalec.GetDeps(spec, targetKey)
	ls := maps.Keys(deps.Build)
	slices.Sort(ls)

	return ls
}

func workerImg(sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) llb.State {
	// TODO: support named context override... also this should possibly be its own image, maybe?
	return llb.Image(workerImgRef, llb.WithMetaResolver(sOpt.Resolver), dalec.WithConstraints(opts...)).
		Run(
			shArgs("apt-get update && apt-get install -y build-essential binutils-mingw-w64 g++-mingw-w64-x86-64 gcc git make pkg-config quilt zip"),
			varCacheAptMount,
			varLibAptMount,
		).Root()
}

func shArgs(cmd string) llb.RunOption {
	return llb.Args([]string{"sh", "-c", cmd})
}

func createBuildScript(spec *dalec.Spec) llb.State {
	buf := bytes.NewBuffer(nil)

	fmt.Fprintln(buf, "#!/usr/bin/env sh")
	fmt.Fprintln(buf, "set -x")

	for i, step := range spec.Build.Steps {
		fmt.Fprintln(buf, "(")

		for k, v := range step.Env {
			fmt.Fprintf(buf, "export %s=%s\n", k, v)
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
