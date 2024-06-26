package rpm

import (
	"bufio"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"text/template"

	"github.com/Azure/dalec"
)

const gomodsName = "__gomods"

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
{{ .Post }}
{{ .PreUn }}
{{ .PostUn }}
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
		constraints := deps.Runtime[name]
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

	if w.Spec.HasGomods() {
		fmt.Fprintf(b, "Source%d: %s.tar.gz\n", len(keys), gomodsName)
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

	prepareGomods := sync.OnceFunc(func() {
		if !w.Spec.HasGomods() {
			return
		}
		fmt.Fprintf(b, "mkdir -p \"%%{_builddir}/%s\"\n", gomodsName)
		fmt.Fprintf(b, "tar -C \"%%{_builddir}/%s\" -xzf \"%%{_sourcedir}/%s.tar.gz\"\n", gomodsName, gomodsName)
	})

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

			prepareGomods()
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

	if w.Spec.HasGomods() {
		fmt.Fprintln(b, "export GOMODCACHE=\"$(pwd)/"+gomodsName+"\"")
	}

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

func (w *specWrapper) PreUn() fmt.Stringer {
	b := &strings.Builder{}

	if w.Spec.Artifacts.Systemd == nil {
		return b
	}

	if len(w.Spec.Artifacts.Systemd.Units) == 0 {
		return b
	}

	b.WriteString("%preun\n")
	keys := dalec.SortMapKeys(w.Spec.Artifacts.Systemd.Units)
	for _, servicePath := range keys {
		serviceName := filepath.Base(servicePath)
		fmt.Fprintf(b, "%%systemd_preun %s\n", serviceName)
	}

	return b
}

func (w *specWrapper) Post() fmt.Stringer {
	b := &strings.Builder{}

	if w.Spec.Artifacts.Systemd == nil {
		return b
	}

	if len(w.Spec.Artifacts.Systemd.Units) == 0 {
		return b
	}

	b.WriteString("%post\n")
	// TODO: can inject other post install steps here in the future

	keys := dalec.SortMapKeys(w.Spec.Artifacts.Systemd.Units)
	for _, servicePath := range keys {
		unitConf := w.Spec.Artifacts.Systemd.Units[servicePath].Artifact()
		fmt.Fprintf(b, "%%systemd_post %s\n", unitConf.ResolveName(servicePath))
	}

	return b
}

func (w *specWrapper) PostUn() fmt.Stringer {
	b := &strings.Builder{}

	if w.Spec.Artifacts.Systemd == nil {
		return b
	}

	if len(w.Spec.Artifacts.Systemd.Units) == 0 {
		return b
	}

	b.WriteString("%postun\n")
	keys := dalec.SortMapKeys(w.Spec.Artifacts.Systemd.Units)
	for _, servicePath := range keys {
		cfg := w.Spec.Artifacts.Systemd.Units[servicePath]
		a := cfg.Artifact()
		serviceName := a.ResolveName(servicePath)
		fmt.Fprintf(b, "%%systemd_postun %s\n", serviceName)
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
		file := cfg.ResolveName(p)
		if !strings.Contains(file, "*") {
			targetPath = filepath.Join(targetDir, file)
		} else {
			targetPath = targetDir + "/"
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

	createArtifactDir := func(root, p string, cfg dalec.ArtifactDirConfig) {
		dir := filepath.Join(root, p)
		mkdirCmd := "mkdir"
		perms := cfg.Mode.Perm()
		if perms != 0 {
			mkdirCmd += fmt.Sprintf(" -m %o", cfg.Mode)
		}
		fmt.Fprintf(b, "%s -p %q\n", mkdirCmd, dir)
	}

	if w.Spec.Artifacts.Directories != nil {
		configKeys := dalec.SortMapKeys(w.Spec.Artifacts.Directories.Config)
		for _, p := range configKeys {
			cfg := w.Spec.Artifacts.Directories.Config[p]
			createArtifactDir(`%{buildroot}/%{_sysconfdir}`, p, cfg)
		}

		stateKeys := dalec.SortMapKeys(w.Spec.Artifacts.Directories.State)
		for _, p := range stateKeys {
			cfg := w.Spec.Artifacts.Directories.State[p]
			createArtifactDir(`%{buildroot}/%{_sharedstatedir}`, p, cfg)
		}
	}

	configKeys := dalec.SortMapKeys(w.Spec.Artifacts.ConfigFiles)
	for _, c := range configKeys {
		cfg := w.Spec.Artifacts.ConfigFiles[c]
		copyArtifact(`%{buildroot}/%{_sysconfdir}`, c, cfg)
	}

	if w.Spec.Artifacts.Systemd != nil {
		serviceKeys := dalec.SortMapKeys(w.Spec.Artifacts.Systemd.Units)
		presetName := "%{name}.preset"
		for _, p := range serviceKeys {
			cfg := w.Spec.Artifacts.Systemd.Units[p]
			// must include systemd unit extension (.service, .socket, .timer, etc.) in name
			copyArtifact(`%{buildroot}/%{_unitdir}`, p, cfg.Artifact())

			verb := "disable"
			if cfg.Enable {
				verb = "enable"
			}

			unitName := filepath.Base(p)
			if cfg.Name != "" {
				unitName = cfg.Name
			}

			fmt.Fprintf(b, "echo '%s %s' >> '%s'\n", verb, unitName, presetName)
		}

		if len(serviceKeys) > 0 {
			copyArtifact(`%{buildroot}/%{_presetdir}`, presetName, dalec.ArtifactConfig{})
		}

		dropinKeys := dalec.SortMapKeys(w.Spec.Artifacts.Systemd.Dropins)
		for _, d := range dropinKeys {
			cfg := w.Spec.Artifacts.Systemd.Dropins[d]
			copyArtifact(`%{buildroot}/%{_unitdir}`, d, cfg.Artifact())
		}
	}

	docKeys := dalec.SortMapKeys(w.Spec.Artifacts.Docs)
	for _, d := range docKeys {
		cfg := w.Spec.Artifacts.Docs[d]
		root := filepath.Join(`%{buildroot}/%{_docdir}`, w.Name)
		copyArtifact(root, d, cfg)
	}

	licenseKeys := dalec.SortMapKeys(w.Spec.Artifacts.Licenses)
	for _, l := range licenseKeys {
		cfg := w.Spec.Artifacts.Licenses[l]
		root := filepath.Join(`%{buildroot}/%{_licensedir}`, w.Name)
		copyArtifact(root, l, cfg)
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
		full := filepath.Join(`%{_bindir}/`, cfg.SubPath, cfg.ResolveName(p))
		fmt.Fprintln(b, full)
	}

	if len(w.Spec.Artifacts.Manpages) > 0 {
		fmt.Fprintln(b, `%{_mandir}/*/*`)
	}

	if w.Spec.Artifacts.Directories != nil {
		configKeys := dalec.SortMapKeys(w.Spec.Artifacts.Directories.Config)
		for _, p := range configKeys {
			dir := strings.Join([]string{`%dir`, filepath.Join(`%{_sysconfdir}`, p)}, " ")
			fmt.Fprintln(b, dir)
		}

		stateKeys := dalec.SortMapKeys(w.Spec.Artifacts.Directories.State)
		for _, p := range stateKeys {
			dir := strings.Join([]string{`%dir`, filepath.Join(`%{_sharedstatedir}`, p)}, " ")
			fmt.Fprintln(b, dir)
		}
	}

	configKeys := dalec.SortMapKeys(w.Spec.Artifacts.ConfigFiles)
	for _, c := range configKeys {
		cfg := w.Spec.Artifacts.ConfigFiles[c]
		fullPath := filepath.Join(`%{_sysconfdir}`, cfg.SubPath, cfg.ResolveName(c))
		fullDirective := strings.Join([]string{`%config(noreplace)`, fullPath}, " ")
		fmt.Fprintln(b, fullDirective)
	}

	if w.Spec.Artifacts.Systemd != nil {
		serviceKeys := dalec.SortMapKeys(w.Spec.Artifacts.Systemd.Units)
		for _, p := range serviceKeys {
			serviceName := filepath.Base(p)
			unitPath := filepath.Join(`%{_unitdir}/`, serviceName)
			fmt.Fprintln(b, unitPath)
		}

		if len(serviceKeys) > 0 {
			fmt.Fprintln(b, "%{_presetdir}/%{name}.preset")
		}

		dropins := make(map[string][]string)
		// process these to get a unique list of files per unit name.
		// we need a single dir entry for the directory
		// need a file entry for each of files
		dropinKeys := dalec.SortMapKeys(w.Spec.Artifacts.Systemd.Dropins)
		for _, d := range dropinKeys {
			cfg := w.Spec.Artifacts.Systemd.Dropins[d]
			art := cfg.Artifact()
			files, ok := dropins[cfg.Unit]
			if !ok {
				files = []string{}
			}
			p := filepath.Join(
				`%{_unitdir}`,
				fmt.Sprintf("%s.d", cfg.Unit),
				art.ResolveName(d),
			)
			dropins[cfg.Unit] = append(files, p)
		}
		unitNames := dalec.SortMapKeys(dropins)
		for _, u := range unitNames {
			dir := strings.Join([]string{
				`%dir`,
				filepath.Join(
					`%{_unitdir}`,
					fmt.Sprintf("%s.d", u),
				),
			}, " ")
			fmt.Fprintln(b, dir)

			for _, file := range dropins[u] {
				fmt.Fprintln(b, file)
			}
		}

	}
	docKeys := dalec.SortMapKeys(w.Spec.Artifacts.Docs)
	for _, d := range docKeys {
		cfg := w.Spec.Artifacts.Docs[d]
		path := filepath.Join(`%{_docdir}`, w.Name, cfg.SubPath, cfg.ResolveName(d))
		fullDirective := strings.Join([]string{`%doc`, path}, " ")
		fmt.Fprintln(b, fullDirective)

	}

	licenseKeys := dalec.SortMapKeys(w.Spec.Artifacts.Licenses)
	for _, l := range licenseKeys {
		cfg := w.Spec.Artifacts.Licenses[l]
		path := filepath.Join(`%{_licensedir}`, w.Name, cfg.SubPath, cfg.ResolveName(l))
		fullDirective := strings.Join([]string{`%license`, path}, " ")
		fmt.Fprintln(b, fullDirective)
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
