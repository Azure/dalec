package windows

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/targets"
	"github.com/Azure/dalec/targets/linux/deb/ubuntu"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

const (
	outputDir       = "/tmp/output"
	buildScriptName = "_build.sh"
	aptCachePrefix  = "jammy-windowscross"
	distroVersionID = ubuntu.JammyVersionID
)

func handleZip(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		sOpt, err := frontend.SourceOptFromClient(ctx, client, nil)
		if err != nil {
			return nil, nil, err
		}

		pg := dalec.ProgressGroup("Build windows container: " + spec.Name)
		worker, err := distroConfig.Worker(sOpt, pg)
		if err != nil {
			return nil, nil, err
		}

		bin, err := buildBinaries(ctx, spec, worker, client, sOpt, targetKey, pg)
		if err != nil {
			return nil, nil, fmt.Errorf("unable to build binaries: %w", err)
		}

		st := getZipLLB(worker, platform, spec, bin, pg)

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

const (
	gomodsName    = "__gomods"
	cargohomeName = "__cargohome"
)

func specToSourcesLLB(worker llb.State, spec *dalec.Spec, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) (map[string]llb.State, error) {
	out, err := dalec.Sources(spec, sOpt, opts...)
	if err != nil {
		return nil, errors.Wrap(err, "error preparign spec sources")
	}

	opts = append(opts, dalec.ProgressGroup("Add gomod sources"))
	gomodSt, err := spec.GomodDeps(sOpt, worker, opts...)
	if err != nil {
		return nil, errors.Wrap(err, "error adding gomod sources")
	}

	cargohomeSt, err := spec.CargohomeDeps(sOpt, worker, opts...)
	if err != nil {
		return nil, errors.Wrap(err, "error adding cargohome sources")
	}

	if gomodSt != nil {
		out[gomodsName] = *gomodSt
	}

	if cargohomeSt != nil {
		out[cargohomeName] = *cargohomeSt
	}

	return out, nil
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

func addGoCache(spec *dalec.Spec, targetKey string) {
	if !spec.HasGomods() && !dalec.HasGolang(spec, targetKey) {
		return
	}

	addCache := true
	for _, c := range spec.Build.Caches {
		if c.GoBuild != nil {
			addCache = false
			break
		}
	}
	if !addCache {
		return
	}

	spec.Build.Caches = append(spec.Build.Caches, dalec.CacheConfig{
		GoBuild: &dalec.GoBuildCache{},
	})
}

func buildBinaries(ctx context.Context, spec *dalec.Spec, worker llb.State, client gwclient.Client, sOpt dalec.SourceOpts, targetKey string, opts ...llb.ConstraintsOpt) (llb.State, error) {
	opts = append(opts, frontend.IgnoreCache(client, targets.IgnoreCacheKeyPkg))
	worker = worker.With(distroConfig.InstallBuildDeps(sOpt, spec, targetKey, opts...))

	sources, err := specToSourcesLLB(worker, spec, sOpt, opts...)
	if err != nil {
		return llb.Scratch(), errors.Wrap(err, "could not generate sources")
	}

	addGoCache(spec, targetKey)

	patched := dalec.PatchSources(worker, spec, sources, opts...)
	buildScript := createBuildScript(spec, opts...)
	artifacts := spec.GetArtifacts(targetKey)
	script := generateInvocationScript(artifacts.Binaries)

	builder := worker.With(dalec.SetBuildNetworkMode(spec))
	st := builder.Run(
		dalec.ShArgs(script.String()),
		llb.Dir("/build"),
		withSourcesMounted("/build", patched, spec.Sources),
		llb.AddMount("/tmp/scripts", buildScript),
		dalec.WithConstraints(opts...),
		// We could check if we even need the var (ie there are gomods) but this
		// is a fine default since we are expecting windows binaries. This
		// means if someone eneds to build non-windows tooling as part of the
		// build then they will need to set GOOS=linux manually.
		// As such, this must come before the env vars from the spec are set.
		llb.AddEnv("GOOS", "windows"),
		dalec.RunOptFunc(func(ei *llb.ExecInfo) {
			for _, c := range spec.Build.Caches {
				c.ToRunOption(worker, path.Join(distroVersionID, targetKey), dalec.WithCacheDirConstraints(opts...)).SetRunOption(ei)
			}
		}),
		dalec.RunOptFunc(func(ei *llb.ExecInfo) {
			for k, v := range spec.Build.Env {
				ei.State = ei.State.With(llb.AddEnv(k, v))
			}
		}),
	).AddMount(outputDir, llb.Scratch())

	return frontend.MaybeSign(ctx, client, st, spec, targetKey, sOpt)
}

func getZipLLB(worker llb.State, platform *ocispecs.Platform, spec *dalec.Spec, artifacts llb.State, opts ...llb.ConstraintsOpt) llb.State {
	fileName := fmt.Sprintf("%s_%s-%s_%s.zip", spec.Name, spec.Version, spec.Revision, platform.Architecture)
	outName := filepath.Join(outputDir, fileName)
	zipped := worker.Run(
		dalec.ShArgs("zip "+outName+" *"),
		llb.Dir("/tmp/artifacts"),
		llb.AddMount("/tmp/artifacts", artifacts),
		dalec.WithConstraints(opts...),
	).AddMount(outputDir, llb.Scratch())
	return zipped
}

func generateInvocationScript(binaries map[string]dalec.ArtifactConfig) *strings.Builder {
	script := &strings.Builder{}
	fmt.Fprintln(script, "#!/usr/bin/env sh")
	fmt.Fprintln(script, "set -ex")
	fmt.Fprintf(script, "/tmp/scripts/%s\n", buildScriptName)
	sorted := dalec.SortMapKeys(binaries)
	for _, bin := range sorted {
		config := binaries[bin]
		fmt.Fprintf(script, "mv '%s' '%s'\n", bin, outputDir)
		if config.Permissions.Perm() != 0 {
			fmt.Fprintf(script, "chmod %o '%s/%s'\n", config.Permissions.Perm(), outputDir, bin)
		}
	}
	return script
}

func createBuildScript(spec *dalec.Spec, opts ...llb.ConstraintsOpt) llb.State {
	buf := bytes.NewBuffer(nil)

	fmt.Fprintln(buf, "#!/usr/bin/env sh")
	fmt.Fprintln(buf, "set -x")

	if spec.HasGomods() {
		fmt.Fprintln(buf, "export GOMODCACHE=\"$(pwd)/"+gomodsName+"\"")
	}

	if spec.HasCargohomes() {
		fmt.Fprintln(buf, "export CARGO_HOME=\"$(pwd)/"+cargohomeName+"\"")
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
