package rpmbundle

import (
	"fmt"
	"strings"
	"sync"
	"text/template"

	"github.com/azure/dalec/frontend"
)

var specTmpl = template.Must(template.New("spec").Parse(strings.TrimSpace(`
Summary: {{.Description}}
Name: {{.Name}}
Version: {{.Version}}
Release: {{.Release}}%{?dist}
License: {{.License}}
URL: {{.Website}}
Vendor: {{.Vendor}}
Packager: {{.Packager}}

{{ .Sources }}
{{ .Conflicts }}

{{- range $p := .Provides }}
Provides: {{$p}}
{{- end }}

{{- range $r := .Replaces }}
Replaces: {{$r}}
{{- end }}

{{ .Requires }}

{{ .PrepareSources }}

%build
{{ .BuildSteps }}

`)))

type specWrapper struct {
	*frontend.Spec
	target           string
	indexSourcesOnce func() map[string]int
}

func newSpecWrapper(spec *frontend.Spec, target string) *specWrapper {
	w := &specWrapper{
		Spec:   spec,
		target: target,
	}
	w.indexSourcesOnce = sync.OnceValue(w.indexSources)
	return w
}

func (w *specWrapper) Requires() fmt.Stringer {
	b := &strings.Builder{}

	satisfies := make(map[string]bool)
	for _, src := range w.Spec.Sources {
		for _, s := range src.Satisfies {
			satisfies[s] = true
		}
	}

	for name, constraints := range w.Spec.Dependencies.Build {
		if satisfies[name] {
			continue
		}
		writeDep(b, "BuildRequires", name, constraints)
	}

	if len(w.Spec.Dependencies.Build) > 0 && len(w.Spec.Dependencies.Runtime) > 0 {
		b.WriteString("\n")
	}

	for name, constraints := range w.Spec.Dependencies.Runtime {
		// satisifes is only for build deps, not runtime deps
		// TODO: consider if it makes sense to support sources satisfying runtime deps
		writeDep(b, "Requires", name, constraints)
	}

	return b
}

func writeDep(b *strings.Builder, kind, name string, constraints []string) {
	if len(constraints) == 0 {
		fmt.Fprintf(b, "%s: %s\n", kind, name)
		return
	}
	for _, c := range constraints {
		fmt.Fprintf(b, "%s: %s %s\n", kind, name, c)
	}
}

func (w *specWrapper) Conflicts() string {
	b := &strings.Builder{}

	for name, constraints := range w.Spec.Conflicts {
		writeDep(b, "Conflicts", name, constraints)
	}
	return b.String()
}

func (w *specWrapper) indexSources() map[string]int {
	// Each source has an index that the rpm spec file uses to refer to it
	// We'll need these indexes when extracting the sources and applying patches
	var idx int

	out := make(map[string]int, len(w.Spec.Sources))
	for name := range w.Spec.Sources {
		out[name] = idx
		idx++
	}
	return out
}

func (w *specWrapper) Sources() (fmt.Stringer, error) {
	b := &strings.Builder{}

	sourceIdx := w.indexSourcesOnce()

	t := w.Spec.Targets[w.target]
	for name := range t.Sources {
		src := w.Spec.Sources[name]
		ref := name
		isDir, err := frontend.SourceIsDir(src)
		if err != nil {
			return nil, fmt.Errorf("error checking if source %s is a directory: %w", name, err)
		}
		if isDir {
			ref += ".tar.gz"
		}

		fmt.Fprintf(b, "Source%d: %s\n", sourceIdx[name], ref)
	}
	return b, nil
}

func (w *specWrapper) Release() string {
	if w.Spec.Revision == "" {
		return "1"
	}
	return w.Spec.Revision
}

func (w *specWrapper) PrepareSources() (fmt.Stringer, error) {
	b := &strings.Builder{}
	t := w.Spec.Targets[w.target]
	if len(t.Sources) == 0 {
		return b, nil
	}

	fmt.Fprintf(b, "%%prep\n")

	patches := make(map[string]bool)

	for _, v := range w.Patches {
		for _, p := range v {
			patches[p] = true
		}
	}

	sourceIndex := w.indexSourcesOnce()

	for name := range t.Sources {
		src := w.Spec.Sources[name]
		err := func(name string, src frontend.Source) error {
			idx := sourceIndex[name]
			if patches[name] {
				// This source is a patch so we don't need to set anything up
				return nil
			}

			isDir, err := frontend.SourceIsDir(src)
			if err != nil {
				return err
			}

			if !isDir {
				fmt.Fprintf(b, "cp -a /SOURCES/%s .\n", name)
				return nil
			}

			fmt.Fprintf(b, "%%setup -T -b %d -q -c -n %s\n", idx, name)

			for _, p := range w.Patches[name] {
				idx, ok := w.indexSourcesOnce()[p]
				if !ok {
					return fmt.Errorf("patch %q not found", p)
				}
				fmt.Fprintf(b, "%%patch%d\n", idx)
			}
			return nil
		}(name, src)
		if err != nil {
			return nil, fmt.Errorf("error preparing source %s: %w", name, err)
		}
	}
	return b, nil
}

func (w *specWrapper) BuildSteps() (fmt.Stringer, error) {
	b := &strings.Builder{}

	steps := w.Spec.Targets[w.target]

	b.WriteString("_rootdir=$(pwd)")
	if steps.WorkDir != "" {
		fmt.Fprintf(b, "mkdir -p %s\n", "${_rootdir}/"+steps.WorkDir)
		fmt.Fprintf(b, "cd %s\n", "${_rootdir}/"+steps.WorkDir)
		fmt.Fprint(b, "\n")
	}

	for k, v := range steps.Env {
		fmt.Fprintf(b, "export %s=%s\n", k, v)
	}

	for _, step := range steps.Steps {
		for k, v := range step.Env {
			fmt.Fprintf(b, "%s=%s ", k, v)
		}
		fmt.Fprintln(b, step.Command)
	}

	return b, nil
}
