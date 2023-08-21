package rpmbundle

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/azure/dalec/frontend"
)

var specTmpl = template.Must(template.New("spec").Parse(`
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

%prep
{{ .PrepareSources }}

`))

type specWrapper struct {
	*frontend.Spec
}

func (w *specWrapper) Requires() string {
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

	return b.String()
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

func (w *specWrapper) Sources() (string, error) {
	b := &strings.Builder{}

	var idx int
	for name, src := range w.Spec.Sources {
		ref := name
		isDir, err := frontend.SourceIsDir(src)
		if err != nil {
			return "", fmt.Errorf("error checking if source %s is a directory: %w", name, err)
		}
		if isDir {
			ref += ".tar.gz"
		}

		fmt.Fprintf(b, "Source%d: %s\n", idx, ref)
		idx++
	}
	return b.String(), nil
}

func (w *specWrapper) Release() string {
	if w.Spec.Revision == "" {
		return "1"
	}
	return w.Spec.Revision
}

func (w *specWrapper) PrepareSources() (fmt.Stringer, error) {
	b := &strings.Builder{}

	var idx int
	for name, src := range w.Spec.Sources {
		err := func() error {
			defer func() {
				idx++
			}()

			isDir, err := frontend.SourceIsDir(src)
			if err != nil {
				return err
			}

			if !isDir {
				fmt.Fprintf(b, "cp -a /SOURCES/%s .\n", name)
				return nil
			}
			if idx == 0 {
				_, _ = fmt.Fprintf(b, "%%setup -q -n %s\n", name)
			} else {
				_, _ = fmt.Fprintf(b, "%%setup -q -n %s -a\n", name)
			}
			return nil
		}()
		if err != nil {
			return nil, fmt.Errorf("error preparing source %s: %w", name, err)
		}
	}
	return b, nil
}
