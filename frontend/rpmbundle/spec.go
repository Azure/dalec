package rpmbundle

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
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
{{ .Provides }}
{{ .Replaces }}
{{ .Requires }}

%description
{{.Description}}

{{ .PrepareSources }}

{{ .BuildSteps }}

{{ .Install }}

{{ .Files }}

%changelog
* Mon Aug 28 2023 Brian Goff <brgoff@microsoft.com>
- Dummy changelog entry
`)))

type specWrapper struct {
	*frontend.Spec
}

func newSpecWrapper(spec *frontend.Spec) *specWrapper {
	w := &specWrapper{
		Spec: spec,
	}
	return w
}

func (w *specWrapper) Provides() fmt.Stringer {
	b := &strings.Builder{}

	sort.Strings(w.Spec.Provides)
	for _, name := range w.Spec.Provides {
		fmt.Fprintln(b, "Provides:", name)
	}
	return b
}

func (w *specWrapper) Replaces() fmt.Stringer {
	b := &strings.Builder{}

	keys := sortMapKeys(w.Spec.Replaces)
	for _, name := range keys {
		writeDep(b, "Replaces", name, w.Spec.Replaces[name])
	}
	return b
}

func (w *specWrapper) Requires() fmt.Stringer {
	b := &strings.Builder{}

	satisfies := make(map[string]bool)
	for _, src := range w.Spec.Sources {
		for _, s := range src.Satisfies {
			satisfies[s] = true
		}
	}

	buildKeys := sortMapKeys(w.Spec.Dependencies.Build)
	for _, name := range buildKeys {
		if satisfies[name] {
			continue
		}
		constraints := w.Spec.Dependencies.Build[name]
		writeDep(b, "BuildRequires", name, constraints)
	}

	if len(w.Spec.Dependencies.Build) > 0 && len(w.Spec.Dependencies.Runtime) > 0 {
		b.WriteString("\n")
	}

	runtimeKeys := sortMapKeys(w.Spec.Dependencies.Runtime)
	for _, name := range runtimeKeys {
		constraints := w.Spec.Dependencies.Build[name]
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

	sort.Strings(constraints)
	for _, c := range constraints {
		fmt.Fprintf(b, "%s: %s %s\n", kind, name, c)
	}
}

func (w *specWrapper) Conflicts() string {
	b := &strings.Builder{}

	keys := sortMapKeys(w.Spec.Conflicts)
	for _, name := range keys {
		constraints := w.Spec.Conflicts[name]
		writeDep(b, "Conflicts", name, constraints)
	}
	return b.String()
}

func (w *specWrapper) Sources() (fmt.Stringer, error) {
	b := &strings.Builder{}

	// Sort keys for consistent output
	keys := sortMapKeys(w.Spec.Sources)

	for idx, name := range keys {
		src := w.Spec.Sources[name]
		ref := name
		isDir, err := frontend.SourceIsDir(src)
		if err != nil {
			return nil, fmt.Errorf("error checking if source %s is a directory: %w", name, err)
		}
		if isDir {
			ref += ".tar.gz"
		}

		fmt.Fprintf(b, "Source%d: %s\n", idx, ref)
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
	if len(w.Spec.Sources) == 0 {
		return b, nil
	}

	fmt.Fprintf(b, "%%prep\n")

	patches := make(map[string]bool)

	for _, v := range w.Patches {
		for _, p := range v {
			patches[p] = true
		}
	}

	// Sort keys for consistent output
	keys := sortMapKeys(w.Spec.Sources)

	for _, name := range keys {
		src := w.Spec.Sources[name]
		err := func(name string, src frontend.Source) error {
			if patches[name] {
				// This source is a patch so we don't need to set anything up
				return nil
			}

			isDir, err := frontend.SourceIsDir(src)
			if err != nil {
				return err
			}

			if !isDir {
				fmt.Fprintf(b, "cp -a %%{_sourcedir}/%s .\n", name)
				return nil
			}

			fmt.Fprintf(b, "mkdir -p %%{_builddir}/%s\n", name)
			fmt.Fprintf(b, "tar -C %%{_builddir}/%s -xzf %%{_sourcedir}/%s.tar.gz\n", name, name)

			for _, p := range w.Patches[name] {
				fmt.Fprintf(b, "cd %s\n", name)
				fmt.Fprintf(b, "patch -p0 -s < %%{_sourcedir}/%s\n", p)
			}
			return nil
		}(name, src)
		if err != nil {
			return nil, fmt.Errorf("error preparing source %s: %w", name, err)
		}
	}
	return b, nil
}

func (w *specWrapper) BuildSteps() fmt.Stringer {
	b := &strings.Builder{}

	t := w.Spec.Build
	if len(t.Steps) == 0 {
		return b
	}

	fmt.Fprintln(b, `%build`)

	fmt.Fprintln(b, "set -e")

	envKeys := sortMapKeys(t.Env)
	for _, k := range envKeys {
		v := t.Env[k]
		fmt.Fprintf(b, "export %s=%s\n", k, v)
	}

	for _, step := range t.Steps {
		envKeys := sortMapKeys(step.Env)
		for _, k := range envKeys {
			fmt.Fprintf(b, "%s=%s ", k, step.Env[k])
		}
		fmt.Fprintln(b, step.Command)
	}

	return b
}

func (w *specWrapper) Install() fmt.Stringer {
	b := &strings.Builder{}

	if w.Spec.Artifacts.IsEmpty() {
		return b
	}

	fmt.Fprintln(b, "%install")

	copyArtifact := func(root, p string, cfg frontend.ArtifactConfig) {
		targetDir := filepath.Join(root, cfg.SubPath)
		fmt.Fprintln(b, "mkdir -p", targetDir)

		var targetPath string
		if cfg.Name != "" {
			targetPath = filepath.Join(targetDir, cfg.Name)
		} else {
			baseName := filepath.Base(p)
			if !strings.Contains(baseName, "*") {
				targetPath = filepath.Join(targetDir, baseName)
			} else {
				targetPath = targetDir + "/"
			}
		}
		fmt.Fprintln(b, "cp -r", p, targetPath)
	}

	binKeys := sortMapKeys(w.Spec.Artifacts.Binaries)
	for _, p := range binKeys {
		cfg := w.Spec.Artifacts.Binaries[p]
		copyArtifact(`%{buildroot}/%{_bindir}`, p, cfg)
	}

	manKeys := sortMapKeys(w.Spec.Artifacts.Manpages)
	for _, p := range manKeys {
		cfg := w.Spec.Artifacts.Manpages[p]
		copyArtifact(`%{buildroot}/%{_mandir}`, p, cfg)
	}

	return b
}

func (w *specWrapper) Files() fmt.Stringer {
	b := &strings.Builder{}

	if w.Spec.Artifacts.IsEmpty() {
		return b
	}

	fmt.Fprintln(b, "%files")

	binKeys := sortMapKeys(w.Spec.Artifacts.Binaries)
	for _, p := range binKeys {
		cfg := w.Spec.Artifacts.Binaries[p]
		full := filepath.Join(`%{_bindir}/`, cfg.SubPath, filepath.Base(p))
		fmt.Fprintln(b, full)
	}

	if len(w.Spec.Artifacts.Manpages) > 0 {
		fmt.Fprintln(b, `%{_mandir}/*/*`)
	}
	return b
}
