package dalec

import (
	"encoding/json"
	"path"
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

func WithRemovedDockerCleanFile() llb.RunOption {
	return runOptionFunc(func(ei *llb.ExecInfo) {
		llb.AddMount("/etc/apt/apt.conf.d/docker-clean", llb.Scratch())
	})
}

func WithMountedAptCache(dst, cacheName string) llb.RunOption {
	return runOptionFunc(func(ei *llb.ExecInfo) {
		WithRemovedDockerCleanFile().SetRunOption(ei)
		llb.AddMount(dst, llb.Scratch(), llb.AsPersistentCacheDir(cacheName, llb.CacheMountLocked)).
			SetRunOption(ei)
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

	return runOptFunc(func(ei *llb.ExecInfo) {
		for _, opt := range opts {
			opt.SetRunOption(ei)
		}
	})
}

type runOptFunc func(*llb.ExecInfo)

func (f runOptFunc) SetRunOption(ei *llb.ExecInfo) {
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

func (s *Spec) GetBuildDeps(targetKey string) []string {
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
	for p := range deps.Build {
		out = append(out, p)
	}

	sort.Strings(out)
	return out

}

func (s *Spec) GetSymlinks(target string) map[string]SymlinkTarget {
	lm := make(map[string]SymlinkTarget)

	if s.Image != nil && s.Image.Post != nil && s.Image.Post.Symlinks != nil {
		for k, v := range s.Image.Post.Symlinks {
			lm[k] = v
		}
	}

	tgt, ok := s.Targets[target]
	if !ok {
		return lm
	}

	if tgt.Image != nil && tgt.Image.Post != nil && tgt.Image.Post.Symlinks != nil {
		for k, v := range tgt.Image.Post.Symlinks {
			// target-specific values replace the ones in the spec toplevel
			lm[k] = v
		}
	}

	return lm
}

func (s *Spec) GetSigner(targetKey string) (*Frontend, bool) {
	if s.Targets != nil {
		if t, ok := s.Targets[targetKey]; ok && hasValidSigner(t.PackageConfig) {
			return t.PackageConfig.Signer, true
		}
	}

	if hasValidSigner(s.PackageConfig) {
		return s.PackageConfig.Signer, true
	}

	return nil, false
}

func hasValidSigner(pc *PackageConfig) bool {
	return pc != nil && pc.Signer != nil && pc.Signer.Image != ""
}
