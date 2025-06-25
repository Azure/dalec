package rpm

import (
	"fmt"
	"strings"

	"github.com/Azure/dalec"
	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"
)

func buildScriptSourceState(spec *dalec.Spec) *llb.State {
	if len(spec.Build.Steps) == 0 {
		return nil
	}

	script := buildScript(spec)
	st := llb.Scratch().File(llb.Mkfile("build.sh", 0755, []byte(script)))
	return &st
}

func buildScript(spec *dalec.Spec) string {
	b := &strings.Builder{}

	t := spec.Build
	if len(t.Steps) == 0 {
		return ""
	}

	fmt.Fprintln(b, "#!/bin/sh")
	fmt.Fprintln(b, "set -e")

	if spec.HasGomods() {
		// Older go versions did not have support for the `GOMODCACHE` var
		// This is a hack to try and make the build work by linking the go modules
		// we've already fetched into to module dir under $GOPATH
		// The default GOMODCACHE value is ${GOPATH}/pkg/mod.
		fmt.Fprintf(b, `test -n "$(go env GOMODCACHE)" || (GOPATH="$(go env GOPATH)"; mkdir -p "${GOPATH}/pkg" && ln -s "$(pwd)/%s" "${GOPATH}/pkg/mod")`, gomodsName)
		// Above command does not have a newline due to quoting issues, so add that here.
		fmt.Fprint(b, "\n")

		fmt.Fprintln(b, "export GOMODCACHE=\"$(pwd)/"+gomodsName+"\"")
	}

	if spec.HasCargohomes() {
		// Set CARGO_HOME to point to our prepared cargo cache
		fmt.Fprintln(b, "export CARGO_HOME=\"$(pwd)/"+cargohomeName+"\"")
	}

	if spec.HasPips() {
		// Set PIP environment variables to point to our prepared pip packages
		// Use --break-system-packages since we're in an isolated build container
		fmt.Fprintln(b, "export PIP_FIND_LINKS=\"$(pwd)/"+pipCacheName+"\"")
		fmt.Fprintln(b, "export PYTHONPATH=\"$(pwd)/"+pipCacheName+":${PYTHONPATH}\"")
		fmt.Fprintln(b, "export PIP_BREAK_SYSTEM_PACKAGES=1")
	}

	envKeys := dalec.SortMapKeys(t.Env)
	for _, k := range envKeys {
		v := t.Env[k]
		fmt.Fprintf(b, "export %s=\"%s\"\n", k, v)
	}

	for _, step := range t.Steps {
		writeStep(b, step)
	}

	b.WriteString("\n")
	return b.String()
}

func ToSourcesLLB(worker llb.State, spec *dalec.Spec, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) ([]llb.State, error) {
	sources, err := dalec.Sources(spec, sOpt)
	if err != nil {
		return nil, err
	}
	out := make([]llb.State, 0, len(sources))

	withPG := func(s string) []llb.ConstraintsOpt {
		return append(opts, dalec.ProgressGroup(s))
	}

	gomodSt, err := spec.GomodDeps(sOpt, worker, withPG("Add gomod sources")...)
	if err != nil {
		return nil, errors.Wrap(err, "error adding gomod sources")
	}

	cargohomeSt, err := spec.CargohomeDeps(sOpt, worker, withPG("Add cargohome sources")...)
	if err != nil {
		return nil, errors.Wrap(err, "error adding cargohome sources")
	}

	pipSt, err := spec.PipDeps(sOpt, worker, withPG("Add pip sources")...)
	if err != nil {
		return nil, errors.Wrap(err, "error adding pip sources")
	}

	if gomodSt != nil {
		out = append(out, gomodSt.With(sourceTar(worker, gomodsName, withPG("Tar gomod deps")...)))
	}

	if cargohomeSt != nil {
		out = append(out, cargohomeSt.With(sourceTar(worker, cargohomeName, withPG("Tar cargohome deps")...)))
	}

	if pipSt != nil {
		out = append(out, pipSt.With(sourceTar(worker, pipCacheName, withPG("Tar pip deps")...)))
	}

	srcsWithNodeMods, err := spec.NodeModDeps(sOpt, worker, opts...)
	if err != nil {
		return nil, errors.Wrap(err, "error preparing node deps")
	}

	sorted := dalec.SortMapKeys(sources)
	for _, k := range sorted {
		st := sources[k]
		if _, ok := srcsWithNodeMods[k]; ok {
			st = srcsWithNodeMods[k]
		}
		if dalec.SourceIsDir(spec.Sources[k]) {
			st = st.With(sourceTar(worker, k, withPG("Tar source: "+k)...))
		}
		out = append(out, st)
	}

	scriptSt := buildScriptSourceState(spec)
	if scriptSt != nil {
		out = append(out, *scriptSt)
	}

	return out, nil
}

func sourceTar(worker llb.State, key string, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		return dalec.Tar(worker, in, key+".tar.gz", opts...)
	}
}
