package rpm

import (
	"bufio"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/Azure/dalec"
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
{{- if .NoArch}}
BuildArch: noarch
{{- end}}


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
{{ .Changelog }}
`)))

type specWrapper struct {
	*dalec.Spec
	Target string
}

func (w *specWrapper) Changelog() (fmt.Stringer, error) {
	b := &strings.Builder{}

	if len(w.Spec.Changelog) == 0 {
		return b, nil
	}

	fmt.Fprintf(b, "%%changelog\n")
	for _, log := range w.Spec.Changelog {
		fmt.Fprintln(b, "* "+log.Date.Format("Mon Jan 2 2006")+" "+log.Author)

		for _, change := range log.Changes {
			fmt.Fprintln(b, "- "+change)
		}
	}

	return b, nil
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

	keys := dalec.SortMapKeys(w.Spec.Replaces)
	for _, name := range keys {
		writeDep(b, "Replaces", name, w.Spec.Replaces[name])
	}
	return b
}

func (w *specWrapper) Requires() fmt.Stringer {
	b := &strings.Builder{}

	deps := w.Spec.Targets[w.Target].Dependencies
	if deps == nil {
		deps = w.Spec.Dependencies
	}
	if deps == nil {
		return b
	}

	buildKeys := dalec.SortMapKeys(deps.Build)
	for _, name := range buildKeys {
		constraints := deps.Build[name]
		writeDep(b, "BuildRequires", name, constraints)
	}

	if len(deps.Build) > 0 && len(deps.Runtime) > 0 {
		b.WriteString("\n")
	}

	runtimeKeys := dalec.SortMapKeys(deps.Runtime)
	for _, name := range runtimeKeys {
		constraints := deps.Build[name]
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

	keys := dalec.SortMapKeys(w.Spec.Conflicts)
	for _, name := range keys {
		constraints := w.Spec.Conflicts[name]
		writeDep(b, "Conflicts", name, constraints)
	}
	return b.String()
}

func (w *specWrapper) Sources() (fmt.Stringer, error) {
	b := &strings.Builder{}

	// Sort keys for consistent output
	keys := dalec.SortMapKeys(w.Spec.Sources)

	for idx, name := range keys {
		src := w.Spec.Sources[name]
		ref := name
		isDir := dalec.SourceIsDir(src)

		if isDir {
			ref += ".tar.gz"
		}

		doc, err := src.Doc(name)
		if err != nil {
			return nil, fmt.Errorf("error getting doc for source %s: %w", name, err)
		}

		scanner := bufio.NewScanner(doc)
		for scanner.Scan() {
			fmt.Fprintf(b, "# %s\n", scanner.Text())
		}
		if scanner.Err() != nil {
			return nil, scanner.Err()
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

	for _, v := range w.Spec.Patches {
		for _, p := range v {
			patches[p.Source] = true
		}
	}

	// Sort keys for consistent output
	keys := dalec.SortMapKeys(w.Spec.Sources)

	for _, name := range keys {
		src := w.Spec.Sources[name]
		err := func(name string, src dalec.Source) error {
			if patches[name] {
				// This source is a patch so we don't need to set anything up
				return nil
			}

			isDir := dalec.SourceIsDir(src)

			if !isDir {
				fmt.Fprintf(b, "cp -a \"%%{_sourcedir}/%s\" .\n", name)
				return nil
			}

			fmt.Fprintf(b, "mkdir -p \"%%{_builddir}/%s\"\n", name)
			fmt.Fprintf(b, "tar -C \"%%{_builddir}/%s\" -xzf \"%%{_sourcedir}/%s.tar.gz\"\n", name, name)

			for _, patch := range w.Spec.Patches[name] {
				fmt.Fprintf(b, "patch -d %q -p%d -s < \"%%{_sourcedir}/%s\"\n", name, *patch.Strip, patch.Source)
			}
			return nil
		}(name, src)
		if err != nil {
			return nil, fmt.Errorf("error preparing source %s: %w", name, err)
		}
	}
	return b, nil
}

func writeStep(b *strings.Builder, step dalec.BuildStep) {
	envKeys := dalec.SortMapKeys(step.Env)
	// Wrap commands in a subshell so any environment variables that are set
	// will be available to every command in the BuildStep
	fmt.Fprintln(b, "(") // begin subshell
	for _, k := range envKeys {
		fmt.Fprintf(b, "export %s=\"%s\"\n", k, step.Env[k])
	}
	fmt.Fprintf(b, "%s", step.Command)
	fmt.Fprintln(b, ")") // end subshell
}

func (w *specWrapper) BuildSteps() fmt.Stringer {
	b := &strings.Builder{}

	t := w.Spec.Build
	if len(t.Steps) == 0 {
		return b
	}

	fmt.Fprintf(b, "%%build\n")

	fmt.Fprintln(b, "set -e")

	envKeys := dalec.SortMapKeys(t.Env)
	for _, k := range envKeys {
		v := t.Env[k]
		fmt.Fprintf(b, "export %s=\"%s\"\n", k, v)
	}

	for _, step := range t.Steps {
		writeStep(b, step)
	}

	return b
}

func (w *specWrapper) Install() fmt.Stringer {
	b := &strings.Builder{}

	fmt.Fprintln(b, "%install")
	if w.Spec.Artifacts.IsEmpty() {
		return b
	}

	copyArtifact := func(root, p string, cfg dalec.ArtifactConfig) {
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

	binKeys := dalec.SortMapKeys(w.Spec.Artifacts.Binaries)
	for _, p := range binKeys {
		cfg := w.Spec.Artifacts.Binaries[p]
		copyArtifact(`%{buildroot}/%{_bindir}`, p, cfg)
	}

	manKeys := dalec.SortMapKeys(w.Spec.Artifacts.Manpages)
	for _, p := range manKeys {
		cfg := w.Spec.Artifacts.Manpages[p]
		copyArtifact(`%{buildroot}/%{_mandir}`, p, cfg)
	}

	return b
}

func (w *specWrapper) Files() fmt.Stringer {
	b := &strings.Builder{}

	fmt.Fprintf(b, "%%files\n")
	if w.Spec.Artifacts.IsEmpty() {
		return b
	}

	binKeys := dalec.SortMapKeys(w.Spec.Artifacts.Binaries)
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

// WriteSpec generates an rpm spec from the provided [dalec.Spec] and distro target and writes it to the passed in writer
func WriteSpec(spec *dalec.Spec, target string, w io.Writer) error {
	s := &specWrapper{spec, target}

	err := specTmpl.Execute(w, s)
	if err != nil {
		return fmt.Errorf("error executing spec template: %w", err)
	}
	return nil
}
