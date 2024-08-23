package dalec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"sync/atomic"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/identity"
)

var disableDiffMerge atomic.Bool

// DisableDiffMerge allows disabling the use of [llb.Diff] and [llb.Merge] in favor of [llb.Copy].
// This is needed when the buildkit version does not support [llb.Diff] and [llb.Merge].
//
// Mainly this would be to allow dockerd with the (current)
// standard setup of dockerd which uses "graphdrivers" to work as these ops are not
// supported by the graphdriver backend.
// When this is false and the graphdriver backend is used, the build will fail when buildkit
// checks the capabilities of the backend.
func DisableDiffMerge(v bool) {
	disableDiffMerge.Store(v)
}

type copyOptionFunc func(*llb.CopyInfo)

func (f copyOptionFunc) SetCopyOption(i *llb.CopyInfo) {
	f(i)
}

func WithIncludes(patterns []string) llb.CopyOption {
	return copyOptionFunc(func(i *llb.CopyInfo) {
		i.IncludePatterns = patterns
	})
}

func WithExcludes(patterns []string) llb.CopyOption {
	return copyOptionFunc(func(i *llb.CopyInfo) {
		i.ExcludePatterns = patterns
	})
}

func WithDirContentsOnly() llb.CopyOption {
	return copyOptionFunc(func(i *llb.CopyInfo) {
		i.CopyDirContentsOnly = true
	})
}

type runOptionFunc func(*llb.ExecInfo)

func (f runOptionFunc) SetRunOption(i *llb.ExecInfo) {
	f(i)
}

// WithMountedAptCache gives an [llb.RunOption] that mounts the apt cache directories.
// It uses the given namePrefix as the prefix for the cache keys.
// namePrefix should be distinct per distro version.
func WithMountedAptCache(namePrefix string) llb.RunOption {
	return runOptionFunc(func(ei *llb.ExecInfo) {
		// This is in the "official" docker image for ubuntu/debian.
		// This file prevents us from actually caching anything.
		// To resolve that we delete the file.
		ei.State = ei.State.File(
			llb.Rm("/etc/apt/apt.conf.d/docker-clean", llb.WithAllowNotFound(true)),
			constraintsOptFunc(func(c *llb.Constraints) {
				*c = ei.Constraints
			}),
		)

		llb.AddMount(
			"/var/cache/apt",
			llb.Scratch(),
			llb.AsPersistentCacheDir(namePrefix+"dalec-var-cache-apt", llb.CacheMountLocked),
		).SetRunOption(ei)

		llb.AddMount(
			"/var/lib/apt",
			llb.Scratch(),
			llb.AsPersistentCacheDir(namePrefix+"dalec-var-lib-apt", llb.CacheMountLocked),
		).SetRunOption(ei)
	})
}

func WithRunOptions(opts ...llb.RunOption) llb.RunOption {
	return runOptionFunc(func(ei *llb.ExecInfo) {
		for _, opt := range opts {
			opt.SetRunOption(ei)
		}
	})
}

type constraintsOptFunc func(*llb.Constraints)

func (f constraintsOptFunc) SetConstraintsOption(c *llb.Constraints) {
	f(c)
}

func (f constraintsOptFunc) SetRunOption(ei *llb.ExecInfo) {
	f(&ei.Constraints)
}

func (f constraintsOptFunc) SetLocalOption(li *llb.LocalInfo) {
	f(&li.Constraints)
}

func (f constraintsOptFunc) SetOCILayoutOption(oi *llb.OCILayoutInfo) {
	f(&oi.Constraints)
}

func (f constraintsOptFunc) SetHTTPOption(hi *llb.HTTPInfo) {
	f(&hi.Constraints)
}

func (f constraintsOptFunc) SetImageOption(ii *llb.ImageInfo) {
	f(&ii.Constraints)
}

func (f constraintsOptFunc) SetGitOption(gi *llb.GitInfo) {
	f(&gi.Constraints)
}

func WithConstraints(ls ...llb.ConstraintsOpt) llb.ConstraintsOpt {
	return constraintsOptFunc(func(c *llb.Constraints) {
		for _, opt := range ls {
			opt.SetConstraintsOption(c)
		}
	})
}

func withConstraints(opts []llb.ConstraintsOpt) llb.ConstraintsOpt {
	return WithConstraints(opts...)
}

// SortMapKeys is a convenience generic function to sort the keys of a map[string]T
func SortMapKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func DuplicateMap[K comparable, V any](m map[K]V) map[K]V {
	newM := make(map[K]V, len(m))
	for k, v := range m {
		newM[k] = v
	}

	return newM
}

// MergeAtPath merges the given states into the given destination path in the given input state.
func MergeAtPath(input llb.State, states []llb.State, dest string) llb.State {
	if disableDiffMerge.Load() {
		output := input
		for _, st := range states {
			output = output.
				File(llb.Copy(st, "/", dest, WithCreateDestPath()))
		}
		return output
	}

	diffs := make([]llb.State, 0, len(states)+1)
	diffs = append(diffs, input)

	for _, src := range states {
		st := src
		if dest != "" && dest != "/" {
			st = llb.Scratch().
				File(llb.Copy(src, "/", dest, WithCreateDestPath()))
		}
		diffs = append(diffs, llb.Diff(input, st))
	}
	return llb.Merge(diffs)
}

type localOptionFunc func(*llb.LocalInfo)

func (f localOptionFunc) SetLocalOption(li *llb.LocalInfo) {
	f(li)
}

func localIncludeExcludeMerge(includes []string, excludes []string) localOptionFunc {
	return func(li *llb.LocalInfo) {
		if len(excludes) > 0 {
			if li.ExcludePatterns != "" {
				var ls []string
				if err := json.Unmarshal([]byte(li.ExcludePatterns), &ls); err != nil {
					panic(err)
				}
				excludes = append(excludes, ls...)
			}
			llb.ExcludePatterns(excludes).SetLocalOption(li)
		}

		if len(includes) > 0 {
			if li.IncludePatterns != "" {
				var ls []string
				if err := json.Unmarshal([]byte(li.IncludePatterns), &ls); err != nil {
					panic(err)
				}
				includes = append(includes, ls...)
			}
			llb.IncludePatterns(includes).SetLocalOption(li)
		}
	}
}

// CacheDirsToRunOpt converts the given cache directories into a RunOption.
func CacheDirsToRunOpt(mounts map[string]CacheDirConfig, distroKey, archKey string) llb.RunOption {
	var opts []llb.RunOption

	for p, cfg := range mounts {
		mode, err := sharingMode(cfg.Mode)
		if err != nil {
			panic(err)
		}
		key := cfg.Key
		if cfg.IncludeDistroKey {
			key = path.Join(distroKey, key)
		}

		if cfg.IncludeArchKey {
			key = path.Join(archKey, key)
		}

		opts = append(opts, llb.AddMount(p, llb.Scratch(), llb.AsPersistentCacheDir(key, mode)))
	}

	return RunOptFunc(func(ei *llb.ExecInfo) {
		for _, opt := range opts {
			opt.SetRunOption(ei)
		}
	})
}

type RunOptFunc func(*llb.ExecInfo)

func (f RunOptFunc) SetRunOption(ei *llb.ExecInfo) {
	f(ei)
}

// ProgressGroup creates a progress group with the given name.
// If a progress group is already set in the constraints the id is reused.
// If no progress group is set a new id is generated.
func ProgressGroup(name string) llb.ConstraintsOpt {
	return constraintsOptFunc(func(c *llb.Constraints) {
		if c.Metadata.ProgressGroup != nil {
			id := c.Metadata.ProgressGroup.Id
			llb.ProgressGroup(id, name, false).SetConstraintsOption(c)
			return
		}

		llb.ProgressGroup(identity.NewID(), name, false).SetConstraintsOption(c)
	})
}

func (s *Spec) GetRuntimeDeps(targetKey string) []string {
	var deps *PackageDependencies
	if t, ok := s.Targets[targetKey]; ok {
		deps = t.Dependencies
	}

	if deps == nil {
		deps = s.Dependencies
		if deps == nil {
			return nil
		}
	}

	var out []string
	for p := range deps.Runtime {
		out = append(out, p)
	}

	sort.Strings(out)
	return out

}

func (s *Spec) GetBuildDeps(targetKey string) map[string]PackageConstraints {
	var deps *PackageDependencies
	if t, ok := s.Targets[targetKey]; ok {
		deps = t.Dependencies
	}

	if deps == nil {
		deps = s.Dependencies
		if deps == nil {
			return nil
		}
	}

	return deps.Build
}

func (s *Spec) GetTestDeps(targetKey string) []string {
	var deps *PackageDependencies
	if t, ok := s.Targets[targetKey]; ok {
		deps = t.Dependencies
	}

	if deps == nil {
		deps = s.Dependencies
		if deps == nil {
			return nil
		}
	}

	out := slices.Clone(deps.Test)
	slices.Sort(out)
	return out
}

func (s *Spec) GetImagePost(target string) *PostInstall {
	img := s.Targets[target].Image
	if img != nil {
		if img.Post != nil {
			return img.Post
		}
	}

	if s.Image != nil {
		return s.Image.Post
	}

	return nil
}

// ShArgs returns a RunOption that runs the given command in a shell.
func ShArgs(args string) llb.RunOption {
	return llb.Args(append([]string{"sh", "-c"}, args))
}

// ShArgsf is the same as [ShArgs] but tkes a format string
func ShArgsf(format string, args ...interface{}) llb.RunOption {
	return ShArgs(fmt.Sprintf(format, args...))
}

// InstallPostSymlinks returns a RunOption that adds symlinks defined in the [PostInstall] underneath the provided rootfs path.
func InstallPostSymlinks(post *PostInstall, rootfsPath string) llb.RunOption {
	return runOptionFunc(func(ei *llb.ExecInfo) {
		if post == nil {
			return
		}

		if len(post.Symlinks) == 0 {
			return
		}

		llb.Dir(rootfsPath).SetRunOption(ei)

		buf := bytes.NewBuffer(nil)
		buf.WriteString("set -ex\n")

		for src, tgt := range post.Symlinks {
			fmt.Fprintf(buf, "ln -s %q %q\n", src, filepath.Join(rootfsPath, tgt.Path))
		}

		const name = "tmp.dalec.symlink.sh"
		script := llb.Scratch().File(llb.Mkfile(name, 0o400, buf.Bytes()))

		llb.AddMount(name, script, llb.SourcePath(name)).SetRunOption(ei)
		llb.Args([]string{"/bin/sh", name}).SetRunOption(ei)
		ProgressGroup("Add post-install symlinks").SetRunOption(ei)
	})
}

func (s *Spec) GetSigner(targetKey string) (*PackageSigner, bool) {
	if s.Targets != nil {
		targetOverridesRootSigningConfig := hasValidSigner(s.PackageConfig)

		if t, ok := s.Targets[targetKey]; ok && hasValidSigner(t.PackageConfig) {
			return t.PackageConfig.Signer, targetOverridesRootSigningConfig
		}
	}

	if hasValidSigner(s.PackageConfig) {
		return s.PackageConfig.Signer, false
	}

	return nil, false
}

func hasValidSigner(pc *PackageConfig) bool {
	return pc != nil && pc.Signer != nil && pc.Signer.Image != ""
}

// SortMapValues is like [maps.Values], but the list is sorted based on the map key
func SortedMapValues[T any](m map[string]T) []T {
	keys := SortMapKeys(m)

	out := make([]T, 0, len(keys))
	for _, k := range keys {
		out = append(out, m[k])
	}

	return out
}

// GetPackageDeps returns the package dependencies for the given target.
// If the target does not have dependencies, the global dependencies are returned.
func (s *Spec) GetPackageDeps(target string) *PackageDependencies {
	if deps := s.Targets[target]; deps.Dependencies != nil {
		return deps.Dependencies
	}
	return s.Dependencies
}
