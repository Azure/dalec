package deb

import (
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/Azure/dalec"
	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"
)

const customSystemdPostinstFile = "custom_systemd_postinst.sh.partial"

//go:embed templates/patch-header.txt
var patchHeader []byte

// This creates a directory in the debian root directory for each patch, and copies the patch files into it.
// The format for each patch dir matches what would normaly be under `debian/patches`, just that this is a separate dir for every source we are patching
// This is purely for documenting in the source package how patches are applied in a more readable way than the big merged patch file.
func sourcePatchesDir(sOpt dalec.SourceOpts, base llb.State, dir, name string, spec *dalec.Spec, opts ...llb.ConstraintsOpt) ([]llb.State, error) {
	patchesPath := filepath.Join(dir, name)
	base = base.
		File(llb.Mkdir(patchesPath, 0o755), opts...)

	var states []llb.State

	seriesBuf := bytes.NewBuffer(nil)
	for _, patch := range spec.Patches[name] {
		src := spec.Sources[patch.Source]

		copySrc := patch.Source
		if patch.Path != "" {
			src.Includes = append(src.Includes, patch.Path)
			copySrc = filepath.Base(patch.Path)
		}
		st, err := src.AsState(patch.Source, sOpt, opts...)
		if err != nil {
			return nil, errors.Wrap(err, "error creating patch state")
		}

		st = base.File(llb.Copy(st, copySrc, filepath.Join(patchesPath, patch.Source)), opts...)
		if _, err := seriesBuf.WriteString(name + "\n"); err != nil {
			return nil, errors.Wrap(err, "error writing to series file")
		}
		states = append(states, st)
	}

	series := base.File(llb.Mkfile(filepath.Join(patchesPath, "series"), 0o640, seriesBuf.Bytes()), opts...)

	return append(states, series), nil
}

// Debroot creates a debian root directory suitable for use with debbuild.
// This does not include sources in case you want to mount sources (instead of copying them) later.
func Debroot(sOpt dalec.SourceOpts, spec *dalec.Spec, worker, in llb.State, target, dir string, opts ...llb.ConstraintsOpt) (llb.State, error) {
	control, err := controlFile(spec, in, target, dir)
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

	base := llb.Scratch().File(llb.Mkdir(dir, 0o755), opts...)
	installers := createInstallScripts(worker, spec, dir)

	debian := base.
		File(llb.Mkdir(filepath.Join(dir, "source"), 0o755), opts...).
		With(func(in llb.State) llb.State {
			if len(spec.Sources) == 0 {
				return in
			}
			return in.
				File(llb.Mkfile(filepath.Join(dir, "source/format"), 0o640, []byte("3.0 (quilt)")), opts...).
				File(llb.Mkfile(filepath.Join(dir, "source/options"), 0o640, []byte("create-empty-orig")), opts...)
		}).
		File(llb.Mkdir(filepath.Join(dir, "dalec"), 0o755), opts...).
		File(llb.Mkfile(filepath.Join(dir, "source/include-binaries"), 0o640, append([]byte("dalec"), '\n')), opts...)

	states := []llb.State{control, rules, changelog, debian}
	states = append(states, installers...)

	dalecDir := base.
		File(llb.Mkdir(filepath.Join(dir, "dalec"), 0o755), opts...)

	states = append(states, dalecDir.File(llb.Mkfile(filepath.Join(dir, "dalec/build.sh"), 0o700, createBuildScript(spec)), opts...))
	states = append(states, dalecDir.File(llb.Mkfile(filepath.Join(dir, "dalec/patch.sh"), 0o700, createPatchScript(spec)), opts...))
	states = append(states, dalecDir.File(llb.Mkfile(filepath.Join(dir, "dalec/fix_sources.sh"), 0o700, fixupSources(spec)), opts...))
	states = append(states, dalecDir.File(llb.Mkfile(filepath.Join(dir, "dalec/fix_perms.sh"), 0o700, fixupArtifactPerms(spec)), opts...))

	customEnable, err := customDHInstallSystemdPostinst(spec)
	if err != nil {
		return llb.Scratch(), err
	}
	if len(customEnable) > 0 {
		// This is not meant to be executed on its own and will instead get added
		// to a post inst file, so need to mark this as executable.
		states = append(states, dalecDir.File(llb.Mkfile(filepath.Join(dir, "dalec/"+customSystemdPostinstFile), 0o600, customEnable), opts...))
	}

	patchDir := dalecDir.File(llb.Mkdir(filepath.Join(dir, "dalec/patches"), 0o755), opts...)
	sorted := dalec.SortMapKeys(spec.Patches)
	for _, name := range sorted {
		pls, err := sourcePatchesDir(sOpt, patchDir, filepath.Join(dir, "dalec/patches"), name, spec, opts...)
		if err != nil {
			return llb.Scratch(), errors.Wrapf(err, "error creating patch directory for source %q", name)
		}
		states = append(states, pls...)
	}

	return dalec.MergeAtPath(in, states, "/"), nil
}

func fixupArtifactPerms(spec *dalec.Spec) []byte {
	buf := bytes.NewBuffer(nil)

	fmt.Fprintln(buf, "#!/usr/bin/env sh")
	fmt.Fprintln(buf, "set -ex")
	fmt.Fprintln(buf)

	basePath := filepath.Join("debian", spec.Name)

	if spec.Artifacts.Directories == nil {
		return nil
	}

	sorted := dalec.SortMapKeys(spec.Artifacts.Directories.GetConfig())
	for _, name := range sorted {
		cfg := spec.Artifacts.Directories.Config[name]
		if cfg.Mode.Perm() != 0 {
			p := filepath.Join(basePath, "etc", name)
			fmt.Fprintf(buf, "chmod %o %q\n", cfg.Mode.Perm(), p)
		}
	}

	sorted = dalec.SortMapKeys(spec.Artifacts.Directories.GetState())
	for _, name := range sorted {
		cfg := spec.Artifacts.Directories.State[name]
		if cfg.Mode.Perm() != 0 {
			p := filepath.Join(basePath, "var/lib", name)
			fmt.Fprintf(buf, "chmod %o %q\n", cfg.Mode.Perm(), p)
		}
	}

	return buf.Bytes()
}

// For debian sources
//  1. File backed sources are not in the correct format as expected by dalec.
//  2. Sources with certain characters in the name had to be changed, so we need
//     to bring those back.
//
// This is called from `debian/rules` after the source tarball has been extracted.
func fixupSources(spec *dalec.Spec) []byte {
	buf := bytes.NewBuffer(nil)
	writeScriptHeader(buf)

	// now, we need to find all the sources that are file-backed and fix them up
	for name, src := range spec.Sources {
		dirName := sanitizeSourceKey(name)

		if dalec.SourceIsDir(src) {
			if dirName == name {
				continue
			}
			fmt.Fprintf(buf, "mv '%s' '%s'\n", dirName, name)
			continue
		}

		fmt.Fprintf(buf, "mv '%s/%s' '%s.dalec.tmp' || (ls -lh %q; exit 2)\n", dirName, name, name, dirName)
		fmt.Fprintf(buf, "rm -rf '%s'\n", dirName)
		fmt.Fprintf(buf, "mv '%s.dalec.tmp' '%s'\n", name, name)
		fmt.Fprintln(buf)
	}

	return buf.Bytes()
}

func writeScriptHeader(buf io.Writer) {
	fmt.Fprintln(buf, "#!/usr/bin/env sh")
	fmt.Fprintln(buf)

	fmt.Fprintln(buf, "set -ex")
}

func createPatchScript(spec *dalec.Spec) []byte {
	buf := bytes.NewBuffer(nil)

	writeScriptHeader(buf)

	for name, patches := range spec.Patches {
		for _, patch := range patches {
			p := filepath.Join("${DEBIAN_DIR:=debian}/dalec/patches", name, patch.Source)
			fmt.Fprintf(buf, "patch -d %q -p%d -s < %q\n", name, *patch.Strip, p)
		}
	}

	return buf.Bytes()
}

func createBuildScript(spec *dalec.Spec) []byte {
	buf := bytes.NewBuffer(nil)
	writeScriptHeader(buf)

	sorted := dalec.SortMapKeys(spec.Build.Env)
	for _, k := range sorted {
		v := spec.Build.Env[k]
		fmt.Fprintf(buf, "export %q=%q\n", k, v)
	}

	for _, step := range spec.Build.Steps {
		fmt.Fprintln(buf)
		fmt.Fprintln(buf, "(")

		sorted := dalec.SortMapKeys(step.Env)
		for _, k := range sorted {
			v := step.Env[k]
			fmt.Fprintf(buf, "	export %q=%q\n", k, v)
		}

		fmt.Fprintln(buf, step.Command)
		fmt.Fprintln(buf, ")")
	}

	return buf.Bytes()
}

func createInstallScripts(worker llb.State, spec *dalec.Spec, dir string) []llb.State {
	if spec.Artifacts.IsEmpty() {
		return nil
	}

	states := make([]llb.State, 0, len(spec.Artifacts.Binaries)+len(spec.Artifacts.Manpages))
	base := llb.Scratch().File(llb.Mkdir(dir, 0o755, llb.WithParents(true)))

	installBuf := bytes.NewBuffer(nil)
	writeInstallHeader := sync.OnceFunc(func() {
		fmt.Fprintln(installBuf, "#!/usr/bin/dh-exec")
		fmt.Fprintln(installBuf)
	})

	writeInstall := func(src, dir, name string) {
		writeInstallHeader()

		if filepath.Base(src) != name || strings.Contains(src, "*") {
			fmt.Fprintln(installBuf, src, "=>", filepath.Join(dir, name))
			return
		}

		fmt.Fprintln(installBuf, src, dir+"/")
	}

	if len(spec.Artifacts.Binaries) > 0 {
		sorted := dalec.SortMapKeys(spec.Artifacts.Binaries)
		for _, key := range sorted {
			cfg := spec.Artifacts.Binaries[key]
			writeInstall(key, filepath.Join("/usr/bin", cfg.SubPath), cfg.ResolveName(key))
		}
	}

	if len(spec.Artifacts.ConfigFiles) > 0 {
		buf := bytes.NewBuffer(nil)
		sorted := dalec.SortMapKeys(spec.Artifacts.ConfigFiles)
		for _, p := range sorted {
			cfg := spec.Artifacts.ConfigFiles[p]

			dir := filepath.Join("/etc", cfg.SubPath)
			name := cfg.ResolveName(p)
			writeInstall(p, dir, name)
			fmt.Fprintln(buf, filepath.Join(dir, name))
		}

		// See: https://man7.org/linux/man-pages/man5/deb-conffiles.5.html for tracking config files in packages
		states = append(states, base.File(llb.Mkfile(filepath.Join(dir, "conffiles"), 0o640, buf.Bytes())))
	}

	if len(spec.Artifacts.Manpages) > 0 {
		buf := bytes.NewBuffer(nil)

		sorted := dalec.SortMapKeys(spec.Artifacts.Manpages)
		for _, key := range sorted {
			cfg := spec.Artifacts.Manpages[key]
			if cfg.Name != "" || cfg.SubPath != "" {
				resolved := cfg.ResolveName(key)
				writeInstall(key, filepath.Join("/usr/share/doc/manpages", spec.Name, cfg.SubPath), resolved)
				continue
			}
			fmt.Fprintln(buf, key)
		}
		if buf.Len() > 0 {
			states = append(states, base.File(llb.Mkfile(filepath.Join(dir, spec.Name+".manpages"), 0o640, buf.Bytes())))
		}

	}

	if spec.Artifacts.Directories != nil {
		buf := bytes.NewBuffer(nil)

		sorted := dalec.SortMapKeys(spec.Artifacts.Directories.Config)
		for _, name := range sorted {
			fmt.Fprintln(buf, filepath.Join("/etc", name))
		}

		sorted = dalec.SortMapKeys(spec.Artifacts.Directories.State)
		for _, name := range sorted {
			fmt.Fprintln(buf, filepath.Join("/var/lib", name))
		}

		states = append(states, base.File(llb.Mkfile(filepath.Join(dir, spec.Name+".dirs"), 0o640, buf.Bytes())))
	}

	if len(spec.Artifacts.Docs) > 0 || len(spec.Artifacts.Licenses) > 0 {
		buf := bytes.NewBuffer(nil)

		sorted := dalec.SortMapKeys(spec.Artifacts.Docs)
		for _, key := range sorted {
			cfg := spec.Artifacts.Docs[key]
			resolved := cfg.ResolveName(key)
			if resolved != key || cfg.SubPath != "" {
				writeInstall(key, filepath.Join("/usr/share/doc", spec.Name, cfg.SubPath), resolved)
			} else {
				fmt.Fprintln(buf, key)
			}
		}

		sorted = dalec.SortMapKeys(spec.Artifacts.Licenses)
		for _, key := range sorted {
			cfg := spec.Artifacts.Licenses[key]
			resolved := cfg.ResolveName(key)
			if resolved != key || cfg.SubPath != "" {
				writeInstall(key, filepath.Join("/usr/share/doc", spec.Name, cfg.SubPath), resolved)
			} else {
				fmt.Fprintln(buf, key)
			}
		}

		if buf.Len() > 0 {
			states = append(states, base.File(llb.Mkfile(filepath.Join(dir, spec.Name+".docs"), 0o640, buf.Bytes())))
		}
	}

	if units := spec.Artifacts.Systemd.GetUnits(); len(units) > 0 {
		// deb-systemd will look for service files in DEBIAN/<package-name>[.<service-name>].<unit-type>
		// To handle this we'll create symlinks to the actual unit files in the source.
		// https://manpages.debian.org/testing/debhelper/dh_installsystemd.1.en.html#FILES

		// Maps the base name of a unit, e.g. "foo.service" -> foo, to the list of
		// units that fall under that basename
		// (e.g. "foo.socket" and  "foo.service")
		// We need to track this in cases where some units under a base are
		// enabled and some are not since dh_installsystemd does not support this
		// directly.

		sorted := dalec.SortMapKeys(units)
		for _, key := range sorted {
			cfg := units[key]
			name, suffix := cfg.SplitName(key)
			if name != spec.Name {
				name = spec.Name + "." + name
			}

			name = name + "." + suffix

			// Unforutnately there is not currently any way to create a symlink
			// directory with llb, so we need to use the worker to create the
			// symlink for us.
			st := worker.Run(
				llb.Dir(filepath.Join("/tmp/work", dir)),
				dalec.ShArgs("ln -s ../"+key+" "+name),
			).AddMount("/tmp/work", llb.Scratch())

			states = append(states, st)
		}
	}

	if dropins := spec.Artifacts.Systemd.GetDropins(); len(dropins) > 0 {
		sorted := dalec.SortMapKeys(dropins)
		for _, key := range sorted {
			cfg := dropins[key]
			cfgA := cfg.Artifact()
			name := cfgA.ResolveName(key)

			writeInstall(key, filepath.Join("/lib/systemd/system", cfg.Unit+".d"), name)
		}
	}

	if len(spec.Artifacts.DataDirs) > 0 {
		sorted := dalec.SortMapKeys(spec.Artifacts.DataDirs)
		for _, key := range sorted {
			cfg := spec.Artifacts.DataDirs[key]
			resolved := cfg.ResolveName(key)

			writeInstall(key, filepath.Join("/usr/share", cfg.SubPath), resolved)
		}
	}

	if installBuf.Len() > 0 {
		states = append(states, base.File(llb.Mkfile(filepath.Join(dir, spec.Name+".install"), 0o700, installBuf.Bytes())))
	}

	return states
}

func controlFile(spec *dalec.Spec, in llb.State, target, dir string) (llb.State, error) {
	buf := bytes.NewBuffer(nil)
	info, _ := debug.ReadBuildInfo()
	buf.WriteString("# Automatically generated by " + info.Main.Path + "\n")
	buf.WriteString("\n")

	if dir == "" {
		dir = "debian"
	}

	if err := WriteControl(spec, target, buf); err != nil {
		return llb.Scratch(), err
	}

	return in.
			File(llb.Mkdir(dir, 0o755, llb.WithParents(true))).
			File(llb.Mkfile(filepath.Join(dir, "control"), 0o640, buf.Bytes())),
		nil
}