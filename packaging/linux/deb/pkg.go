package deb

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/Azure/dalec"
	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"
)

const (
	// Unique name that would not normally be in the spec
	// This will get used to create the source tar for go module deps
	gomodsName = "xxxdalecGomodsInternal"
	// Unique name that would not normally be in the spec
	// This will get used to create the source tar for cargo deps
	cargohomeName = "xxxdalecCargoHomeInternal"
)

func mountSources(sources map[string]llb.State, dir string, mod func(string) string) llb.RunOption {
	return dalec.RunOptFunc(func(ei *llb.ExecInfo) {
		for key, src := range sources {
			if mod != nil {
				key = mod(key)
			}
			llb.AddMount(filepath.Join(dir, key), src).SetRunOption(ei)
		}
	})
}

var errMissingRequiredField = fmt.Errorf("missing required field")

func validateSpec(spec *dalec.Spec) error {
	if spec.Packager == "" {
		return errors.Wrap(errMissingRequiredField, "packager")
	}
	return nil
}

// Dalec patches apply directly to each individual source tree, e.g. `cd <src>; patch ...`
// Debian applies patches from 1 directory up from the source tree (e.g. no `cd` as above).
// As such the patch files are not formatted correctly for Debian's build tooling.
// Here we generate a single patch file that generates the correct format.
//
// This way dpkg-source can automatically apply patches for us, and informs
// the caller of the patches applied and is generally just more inline with
// a typical deb build.
//
// This is using git instead of raw `diff` or other standalone tooling because only git appears to preserve permissions for new files.
// As an example, if patch adds a new file with mode +x, `diff` will not see the permissions for that new file.
func createPatches(spec *dalec.Spec, sources map[string]llb.State, worker llb.State, dr llb.State, opts ...llb.ConstraintsOpt) llb.State {
	patches := llb.Scratch()
	if len(spec.Patches) > 0 {
		patchesMountInput := llb.Scratch().
			File(llb.Mkfile("dalec-changes.patch", 0o600, patchHeader))

		patches = worker.
			Run(dalec.ShArgs("set -e; git config --global user.email phony; git config --global user.name Dalec")).
			Run(
				dalec.ShArgs("set -e; git init .; git add .; git commit -m 'Initial commit'; \"${DEBIAN_DIR}/dalec/patch.sh\"; git add .; git commit -m 'With patch'; git diff HEAD~1 >> /work/out/dalec-changes.patch; echo 'dalec-changes.patch' > /work/out/series"),
				llb.Dir("/work/sources"),
				mountSources(sources, "/work/sources", nil),
				// DEBIAN_DIR is used by the patch script to find the debian directory where we actually have the patches
				llb.AddEnv("DEBIAN_DIR", "/work/debian"),
				llb.AddMount("/work/debian", dr, llb.SourcePath("debian"), llb.Readonly),
				dalec.WithConstraints(opts...),
			).AddMount("/work/out", patchesMountInput)
	}

	return patches
}

func SourcePackage(ctx context.Context, sOpt dalec.SourceOpts, worker llb.State, spec *dalec.Spec, targetKey, distroVersionID string, cfg SourcePkgConfig, opts ...llb.ConstraintsOpt) (llb.State, error) {
	if err := validateSpec(spec); err != nil {
		return llb.Scratch(), err
	}
	dr, err := Debroot(ctx, sOpt, spec, worker, llb.Scratch(), targetKey, "", distroVersionID, cfg, opts...)
	if err != nil {
		return llb.Scratch(), err
	}

	sources, err := dalec.Sources(spec, sOpt)
	if err != nil {
		return llb.Scratch(), err
	}

	gomodSt, err := spec.GomodDeps(sOpt, worker, opts...)
	if err != nil {
		return llb.Scratch(), errors.Wrap(err, "error preparing gomod deps")
	}

	cargohomeSt, err := spec.CargohomeDeps(sOpt, worker, opts...)
	if err != nil {
		return llb.Scratch(), errors.Wrap(err, "error preparing cargohome deps")
	}

	srcsWithNodeMods, err := spec.NodeModDeps(sOpt, worker, opts...)
	if err != nil {
		return llb.Scratch(), errors.Wrap(err, "error preparing node deps")
	}
	sorted := dalec.SortMapKeys(srcsWithNodeMods)

	for _, key := range sorted {
		sources[key] = srcsWithNodeMods[key]
	}

	if gomodSt != nil {
		sources[gomodsName] = *gomodSt
	}

	if cargohomeSt != nil {
		sources[cargohomeName] = *cargohomeSt
	}

	patches := createPatches(spec, sources, worker, dr, opts...)

	work := worker.Run(
		dalec.ShArgs("set -e; dpkg-buildpackage -S -us -uc; mkdir -p /tmp/out; cp -r /work/"+spec.Name+"_"+spec.Version+"* /tmp/out"),
		llb.Dir("/work/pkg"),
		llb.AddMount("/work/pkg/debian", dr, llb.SourcePath("debian")), // This cannot be readonly because the debian directory gets modified by dpkg-buildpackage
		llb.AddMount("/work/pkg/debian/patches", patches, llb.Readonly),
		dalec.RunOptFunc(func(ei *llb.ExecInfo) {
			debSources := TarDebSources(worker, spec, sources, "src.tar.gz", sOpt, opts...)
			llb.AddMount("/work/"+spec.Name+"_"+spec.Version+".orig.tar.gz", debSources, llb.SourcePath("src.tar.gz")).SetRunOption(ei)
		}),
		dalec.WithConstraints(opts...),
	)

	return work.AddMount("/tmp/out", llb.Scratch()), nil
}

func BuildDebBinaryOnly(worker llb.State, spec *dalec.Spec, debroot llb.State, distroVersionID string, opts ...llb.ConstraintsOpt) (llb.State, error) {
	dirName := filepath.Join("/work", spec.Name+"_"+spec.Version+"-"+spec.Revision)
	st := worker.
		Run(
			dalec.ShArgs("set -e; dpkg-buildpackage -b -uc -us; mkdir -p /tmp/out; cp ../*.deb /tmp/out"),
			llb.Dir(dirName),
			llb.AddMount(dirName, debroot),
			dalec.WithConstraints(opts...),
		).AddMount("/tmp/out", llb.Scratch())

	return dalec.MergeAtPath(llb.Scratch(), []llb.State{st}, "/"), nil
}

func BuildDeb(worker llb.State, spec *dalec.Spec, srcPkg llb.State, distroVersionID string, opts ...llb.ConstraintsOpt) (llb.State, error) {
	dirName := filepath.Join("/work", spec.Name+"_"+spec.Version+"-"+spec.Revision)
	buildRootRel := spec.Name + "-" + spec.Version
	st := worker.
		Run(
			dalec.ShArgs("set -e; dpkg-source -x ./*.dsc; cd "+buildRootRel+"; dpkg-buildpackage -b -uc -us; mkdir -p /tmp/out; cp ../*.deb /tmp/out"),
			llb.Dir(dirName),
			llb.AddMount(dirName, srcPkg),
			dalec.WithConstraints(opts...),
			dalec.RunOptFunc(func(ei *llb.ExecInfo) {
				opts := []dalec.CacheConfigOption{
					dalec.WithCacheDirConstraints(opts...),
				}
				for _, cache := range spec.Build.Caches {
					cache.ToRunOption(distroVersionID, opts...).SetRunOption(ei)
				}
			}),
		).AddMount("/tmp/out", llb.Scratch())

	return dalec.MergeAtPath(llb.Scratch(), []llb.State{st, srcPkg}, "/"), nil
}

func TarDebSources(work llb.State, spec *dalec.Spec, srcStates map[string]llb.State, dest string, sOpts dalec.SourceOpts, opts ...llb.ConstraintsOpt) llb.State {
	outBase := "/tmp/out"
	out := filepath.Join(outBase, filepath.Dir(dest))

	worker := work.Run(
		llb.AddMount("/src", llb.Scratch()),
		dalec.RunOptFunc(func(ei *llb.ExecInfo) {
			for key, state := range srcStates {

				mountOpts := []llb.MountOption{}
				src, ok := spec.Sources[key]

				// If the source is not explicitly listed in the spec sources, assume it is a directory (e.g., for gomod dependencies)
				isDir := true
				if ok {
					isDir = dalec.SourceIsDir(src)
				}

				if !isDir {
					mountOpts = append(mountOpts, llb.SourcePath(filepath.Join("/", key)))
				}

				// If the tar contains only a single directory, dpkg will extract its contents directly into the root directory.
				mounthPath := filepath.Join("/src", key)
				if len(srcStates) == 1 && isDir {
					mounthPath = filepath.Join("/src", key, key)
				}

				llb.AddMount(mounthPath, state, mountOpts...).SetRunOption(ei)
			}
		}),
		dalec.ShArgs("tar -C /src -cvzf /tmp/st ."),
		dalec.WithConstraints(opts...),
	).Run(
		llb.Args([]string{"/bin/sh", "-c", "mkdir -p " + out + " && mv /tmp/st " + filepath.Join(out, filepath.Base(dest))}),
		dalec.WithConstraints(opts...),
	)

	return worker.AddMount(outBase, llb.Scratch())
}
