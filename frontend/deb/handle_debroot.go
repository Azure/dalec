package deb

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/pkg/errors"
	"golang.org/x/exp/maps"
)

func DebrootHandler(target string) frontend.BuildFunc {
	return func(ctx context.Context, client gwclient.Client, spec *dalec.Spec) (gwclient.Reference, *frontend.Image, error) {
		st, err := Debroot(spec, llb.Scratch(), target, "")
		if err != nil {
			return nil, nil, err
		}

		sOpt, err := frontend.SourceOptFromClient(ctx, client)
		if err != nil {
			return nil, nil, err
		}

		sm, err := dalec.Sources(spec, sOpt)
		if err != nil {
			return nil, nil, err
		}

		for k, src := range sm {
			sm[k] = llb.Scratch().File(llb.Copy(src, "/", k, dalec.WithCreateDestPath()))
		}

		sources := maps.Values(sm)
		st = dalec.MergeAtPath(llb.Scratch(), append(sources, st), "/")

		def, err := st.Marshal(ctx)
		if err != nil {
			return nil, nil, errors.Wrap(err, "error marshalling llb")
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
		return ref, &frontend.Image{}, nil
	}
}

// Debroot creates a debian root directory suitable for use with debbuild.
// This does not include sources in case you want to mount sources (instead of copying them) later.
func Debroot(spec *dalec.Spec, in llb.State, target, dir string) (llb.State, error) {
	control, err := Dalec2ControlLLB(spec, in, target, dir)
	if err != nil {
		return llb.Scratch(), errors.Wrap(err, "error generating control file")
	}

	rules, err := Rules(spec, in, dir)
	if err != nil {
		return llb.Scratch(), errors.Wrap(err, "error generating rules file")
	}

	changelog, err := Changelog(spec, in, target, dir)
	if err != nil {
		return llb.Scratch(), errors.Wrap(err, "error generating changelog file")
	}

	if dir == "" {
		dir = "debian"
	}
	compat := llb.Scratch().
		File(llb.Mkdir(dir, 0o755, llb.WithParents(true))).
		File(llb.Mkfile(filepath.Join(dir, "compat"), 0o770, []byte("10")))

	buildScript := createBuildScript(spec)
	installers := createInstallScripts(spec, dir)

	states := []llb.State{control, rules, changelog, compat, buildScript}
	states = append(states, installers...)

	return dalec.MergeAtPath(in, states, "/"), nil
}

func createBuildScript(spec *dalec.Spec) llb.State {
	buf := bytes.NewBuffer(nil)

	fmt.Fprintln(buf, "#!/usr/bin/env sh")
	fmt.Fprintln(buf, "ls -lh")
	fmt.Fprintln(buf, "echo $(pwd)")

	for i, step := range spec.Build.Steps {
		fmt.Fprintln(buf, "(")

		for k, v := range step.Env {
			fmt.Fprintf(buf, "export %s=%s\n", k, v)
		}

		fmt.Fprintln(buf, step.Command)
		fmt.Fprintln(buf, ")")

		if i < len(spec.Build.Steps)-1 {
			fmt.Fprintln(buf, "&& \\")
		}
	}

	return llb.Scratch().
		File(llb.Mkfile("_build", 0o770, buf.Bytes()))
}

func createInstallScripts(spec *dalec.Spec, dir string) []llb.State {
	if len(spec.Artifacts.Binaries) == 0 && len(spec.Artifacts.Manpages) == 0 {
		return nil
	}

	states := make([]llb.State, 0, len(spec.Artifacts.Binaries)+len(spec.Artifacts.Manpages))
	base := llb.Scratch().File(llb.Mkdir(dir, 0o755, llb.WithParents(true)))

	if len(spec.Artifacts.Binaries) > 0 {
		buf := bytes.NewBuffer(nil)
		for p, cfg := range spec.Artifacts.Binaries {
			fmt.Fprintln(buf, p, filepath.Join("usr/bin", cfg.SubPath, cfg.Name))
		}
		states = append(states, base.File(llb.Mkfile(filepath.Join(dir, spec.Name+".install"), 0o640, buf.Bytes())))
	}

	if len(spec.Artifacts.Manpages) > 0 {
		buf := bytes.NewBuffer(nil)
		for p := range spec.Artifacts.Manpages {
			// This doesn't support subpaths or custom names
			// Really it wouldn't generally make sense to... but maybe subpaths could work, but we'd need to use something other than the deb `.manpages` file
			fmt.Fprintln(buf, p)
		}
		states = append(states, base.File(llb.Mkfile(filepath.Join(dir, spec.Name+".manpages"), 0o640, buf.Bytes())))
	}

	return states
}
