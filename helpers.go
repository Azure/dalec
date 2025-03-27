package dalec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"sync/atomic"

	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/util/system"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	// This is used as the source name for sources in specified in `SourceMount`
	// For any sources we need to mount we need to give the source a name.
	// We don't actually care about the name here *except* the way file-backed
	// sources work the name of the file becomes the source name.
	// So we at least need to track it.
	// Source names must also not contain path separators or it can screw up the logic.
	//
	// To note, the name of the source affects how the source is cached, so this
	// should just be a single specific name so we can get maximal cache re-use.
	internalMountSourceName = "src"
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

func WithConstraint(in *llb.Constraints) llb.ConstraintsOpt {
	return constraintsOptFunc(func(c *llb.Constraints) {
		*c = *in
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
	deps := s.GetPackageDeps(targetKey)
	if deps == nil {
		return nil
	}

	return SortMapKeys(deps.Runtime)
}

func (s *Spec) GetBuildDeps(targetKey string) map[string]PackageConstraints {
	deps := s.GetPackageDeps(targetKey)
	if deps == nil {
		return nil
	}

	return deps.Build
}

func (s *Spec) GetTestDeps(targetKey string) []string {
	deps := s.GetPackageDeps(targetKey)
	if deps == nil {
		return nil
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

func (s *Spec) GetArtifacts(targetKey string) Artifacts {
	if t, ok := s.Targets[targetKey]; ok {
		// If unset then we should use the global artifacts but if set or deliberately empty then we should use that.
		if t.Artifacts != nil {
			return *t.Artifacts
		}
	}
	return s.Artifacts
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

		sortedKeys := SortMapKeys(post.Symlinks)
		for _, oldpath := range sortedKeys {
			newpaths := post.Symlinks[oldpath].Paths
			sort.Strings(newpaths)

			for _, newpath := range newpaths {
				fmt.Fprintf(buf, "mkdir -p %q\n", filepath.Join(rootfsPath, filepath.Dir(newpath)))
				fmt.Fprintf(buf, "ln -s %q %q\n", oldpath, filepath.Join(rootfsPath, newpath))
			}
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

// MergeDependencies merges two sets of package dependencies, a base and a target.
// If a dependency is set in both, the one from `target` is used, otherwise, the dependency from parent is used.
// MergeDependencies(nil, child) = child, MergeDependencies(parent, nil) = parent
func MergeDependencies(base, target *PackageDependencies) *PackageDependencies {
	var (
		build      map[string]PackageConstraints
		runtime    map[string]PackageConstraints
		recommends map[string]PackageConstraints
		test       []string
		extraRepos []PackageRepositoryConfig
	)

	if base == nil {
		return target
	}

	if target == nil {
		return base
	}

	if len(target.Build) > 0 {
		build = target.Build
	} else {
		build = base.Build
	}

	if len(target.Runtime) > 0 {
		runtime = target.Runtime
	} else {
		runtime = base.Runtime
	}

	if len(target.Recommends) > 0 {
		recommends = target.Recommends
	} else {
		recommends = base.Recommends
	}

	if len(target.Test) > 0 {
		test = target.Test
	} else {
		test = base.Test
	}

	if len(target.ExtraRepos) > 0 {
		extraRepos = target.ExtraRepos
	} else {
		extraRepos = base.ExtraRepos
	}

	return &PackageDependencies{
		Build:      build,
		Runtime:    runtime,
		Recommends: recommends,
		Test:       test,
		ExtraRepos: extraRepos,
	}
}

// GetPackageDeps returns the package dependencies for the given target.
// If the target does not have dependencies, the global dependencies are returned.
func (s *Spec) GetPackageDeps(target string) *PackageDependencies {
	if _, ok := s.Targets[target]; !ok {
		return s.Dependencies
	}

	return MergeDependencies(s.Dependencies, s.Targets[target].Dependencies)
}

type gitOptionFunc func(*llb.GitInfo)

func (f gitOptionFunc) SetGitOption(gi *llb.GitInfo) {
	f(gi)
}

type RepoPlatformConfig struct {
	ConfigRoot string
	GPGKeyRoot string
	ConfigExt  string
}

// Returns a run option which mounts the data dirs for all specified repos
func WithRepoData(repos []PackageRepositoryConfig, sOpts SourceOpts, opts ...llb.ConstraintsOpt) (llb.RunOption, error) {
	var repoMountsOpts []llb.RunOption
	for _, repo := range repos {
		rs, err := repoDataAsMount(repo, sOpts, opts...)
		if err != nil {
			return nil, err
		}
		repoMountsOpts = append(repoMountsOpts, rs)
	}

	return WithRunOptions(repoMountsOpts...), nil
}

// Returns a run option for mounting the state (i.e., packages/metadata) for a single repo
func repoDataAsMount(config PackageRepositoryConfig, sOpts SourceOpts, opts ...llb.ConstraintsOpt) (llb.RunOption, error) {
	var mounts []llb.RunOption

	for _, data := range config.Data {
		repoState, err := data.Spec.AsMount(internalMountSourceName, sOpts, opts...)
		if err != nil {
			return nil, err
		}
		if SourceIsDir(data.Spec) {
			mounts = append(mounts, llb.AddMount(data.Dest, repoState))
		} else {
			mounts = append(mounts, llb.AddMount(data.Dest, repoState, llb.SourcePath(internalMountSourceName)))
		}
	}

	return WithRunOptions(mounts...), nil
}

func repoConfigAsMount(config PackageRepositoryConfig, platformCfg *RepoPlatformConfig, sOpt SourceOpts, opts ...llb.ConstraintsOpt) ([]llb.RunOption, error) {
	repoConfigs := []llb.RunOption{}

	for name, repoConfig := range config.Config {
		// each of these sources represent a repo config file
		repoConfigSt, err := repoConfig.AsMount(name, sOpt, append(opts, ProgressGroup("Importing repo config: "+name))...)
		if err != nil {
			return nil, err
		}

		var normalized string = name
		if filepath.Ext(normalized) != platformCfg.ConfigExt {
			normalized += platformCfg.ConfigExt
		}

		repoConfigs = append(repoConfigs,
			llb.AddMount(filepath.Join(platformCfg.ConfigRoot, normalized), repoConfigSt, llb.SourcePath(name)))
	}

	return repoConfigs, nil
}

// Returns a run option for importing the config files for all repos
func WithRepoConfigs(repos []PackageRepositoryConfig, cfg *RepoPlatformConfig, sOpt SourceOpts, opts ...llb.ConstraintsOpt) (llb.RunOption, error) {
	configStates := []llb.RunOption{}
	for _, repo := range repos {
		mnts, err := repoConfigAsMount(repo, cfg, sOpt, opts...)
		if err != nil {
			return nil, err
		}

		configStates = append(configStates, mnts...)
	}

	return WithRunOptions(configStates...), nil
}

func GetRepoKeys(configs []PackageRepositoryConfig, cfg *RepoPlatformConfig, sOpt SourceOpts, opts ...llb.ConstraintsOpt) (llb.RunOption, []string, error) {
	keys := []llb.RunOption{}
	names := []string{}
	for _, config := range configs {
		for name, repoKey := range config.Keys {
			gpgKey, err := repoKey.AsMount(name, sOpt, append(opts, ProgressGroup("Fetching repo key: "+name))...)
			if err != nil {
				return nil, nil, err
			}

			mountPath := filepath.Join(cfg.GPGKeyRoot, name)

			keys = append(keys, llb.AddMount(mountPath, gpgKey, llb.SourcePath(name)))
			names = append(names, name)
		}
	}

	return WithRunOptions(keys...), names, nil
}

const (
	netModeNone    = "none"
	netModeSandbox = "sandbox"
)

// SetBuildNetworkMode returns an [llb.StateOption] that determines which
func SetBuildNetworkMode(spec *Spec) llb.StateOption {
	switch spec.Build.NetworkMode {
	case "", netModeNone:
		return llb.Network(llb.NetModeNone)
	case netModeSandbox:
		return llb.Network(llb.NetModeSandbox)
	default:
		return func(in llb.State) llb.State {
			return in.Async(func(context.Context, llb.State, *llb.Constraints) (llb.State, error) {
				return in, fmt.Errorf("invalid build network mode %q", spec.Build.NetworkMode)
			})
		}
	}
}

// BaseImageConfig provides a default image config that can be used for
// producing images.
//
// This is taken from https://github.com/moby/buildkit/blob/0655923d7e2884a0d514313fd688178a6da57b43/frontend/dockerfile/dockerfile2llb/image.go#L26-L39
func BaseImageConfig(platform *ocispecs.Platform) *DockerImageSpec {
	img := &DockerImageSpec{}

	if platform == nil {
		p := platforms.DefaultSpec()
		platform = &p
	}

	img.Architecture = platform.Architecture
	img.OS = platform.OS
	img.OSVersion = platform.OSVersion
	if platform.OSFeatures != nil {
		img.OSFeatures = append([]string{}, platform.OSFeatures...)
	}
	img.Variant = platform.Variant
	img.RootFS.Type = "layers"
	img.Config.WorkingDir = "/"
	img.Config.Env = []string{"PATH=" + system.DefaultPathEnv(platform.OS)}

	return img
}

// Platform returns a [llb.ConstraintsOpt] that sets the platform to the provided platform
// If the platform is nil, the [llb.ConstraintOpt] is a no-op.
func Platform(platform *ocispecs.Platform) llb.ConstraintsOpt {
	if platform == nil {
		return constraintsOptFunc(func(c *llb.Constraints) {})
	}
	return llb.Platform(*platform)
}
