package dalec

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	cacheMountShared  = "shared"  // llb.CacheMountShared
	cacheMountLocked  = "locked"  // llb.CacheMountLocked
	cacheMountPrivate = "private" // llb.CacheMountPrivate
	cacheMountUnset   = ""
)

// CacheConfig configures a cache to use for a build.
//
// Other, cache types may be added in the future, such as:
// - rust compiler cache
// - bazel cache
// ...
type CacheConfig struct {
	// Dir specifies a generic cache directory configuration.
	Dir *CacheDir `json:"dir,omitempty" yaml:"dir,omitempty" jsonschema:"oneof_required=dir"`
	// GoBuild specifies a cache for Go's incremental build artifacts.
	// This should speed up repeated builds of Go projects.
	GoBuild *GoBuildCache `json:"gobuild,omitempty" yaml:"gobuild,omitempty" jsonschema:"oneof_required=gobuild"`
}

type CacheInfo struct {
	DirInfo CacheDirInfo
	GoBuild GoBuildCacheInfo
}

type CacheDirInfo struct {
	// Platform sets the platform used to generate part of the cache key when
	// CacheDir.NoAutoNamespace is set to false.
	Platform *ocispecs.Platform
}

type CacheConfigOption interface {
	SetCacheConfigOption(*CacheInfo)
}

type CacheConfigOptionFunc func(*CacheInfo)

func (f CacheConfigOptionFunc) SetCacheConfigOption(info *CacheInfo) {
	f(info)
}

type CacheDirOption interface {
	SetCacheDirOption(*CacheDirInfo)
}

type CacheDirOptionFunc func(*CacheDirInfo)

func (f CacheDirOptionFunc) SetCacheDirOption(info *CacheDirInfo) {
	f(info)
}

func WithCacheDirConstraints(opts ...llb.ConstraintsOpt) CacheConfigOption {
	return CacheConfigOptionFunc(func(info *CacheInfo) {
		var c llb.Constraints
		for _, opt := range opts {
			opt.SetConstraintsOption(&c)
		}
		info.DirInfo.Platform = c.Platform
	})
}

func (c *CacheConfig) ToRunOption(distroKey string, opts ...CacheConfigOption) llb.RunOption {
	if c.Dir != nil {
		return c.Dir.ToRunOption(distroKey, CacheDirOptionFunc(func(info *CacheDirInfo) {
			var cacheInfo CacheInfo
			for _, opt := range opts {
				opt.SetCacheConfigOption(&cacheInfo)
			}
			*info = cacheInfo.DirInfo
		}))
	}

	if c.GoBuild != nil {
		return c.GoBuild.ToRunOption(distroKey, GoBuildCacheOptionFunc(func(info *GoBuildCacheInfo) {
			var cacheInfo CacheInfo
			for _, opt := range opts {
				opt.SetCacheConfigOption(&cacheInfo)
			}
			*info = cacheInfo.GoBuild
		}))
	}

	// Should not reach this point
	panic("invalid cache config")
}

func (c *CacheConfig) validate() error {
	if c == nil {
		return nil
	}

	if (c.Dir == nil && c.GoBuild == nil) || (c.Dir != nil && c.GoBuild != nil) {
		return fmt.Errorf("invalid cache config: one of (and only one of) dir or gobuild must be set")
	}

	var errs []error
	if c.Dir != nil {
		if err := c.Dir.validate(); err != nil {
			errs = append(errs, fmt.Errorf("invalid cache dir config: %w", err))
		}
	}
	if c.GoBuild != nil {
		if err := c.GoBuild.validate(); err != nil {
			errs = append(errs, fmt.Errorf("invalid go build cache config: %w", err))
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// CacheDir is a generic cache directory configuration.
type CacheDir struct {
	// Key is the cache key to use.
	// If not set then the dest will be used.
	Key string `json:"key" yaml:"key"`
	// Dest is the directory to mount the cache to.
	Dest string `json:"dest" yaml:"dest" jsonschema:"required"`
	// Sharing is the sharing mode of the cache.
	// It can be one of the following:
	// - shared: multiple jobs can use the cache at the same time.
	// - locked: exclusive access to the cache is required.
	// - private: changes to the cache are not shared with other jobs and are discarded
	//   after the job is finished.
	Sharing string `json:"sharing" yaml:"sharing" jsonschema:"enum=shared,enum=locked,enum=private"`

	// NoAutoNamespace disables the automatic prefixing of the cache key with the
	// target specific information such as distro and CPU architecture, which may
	// be auto-injected to prevent common issues that would cause an invalid cache.
	NoAutoNamespace bool `json:"no_auto_namespace" yaml:"no_auto_namespace"`
}

func (c *CacheDir) ToRunOption(distroKey string, opts ...CacheDirOption) llb.RunOption {
	return RunOptFunc(func(ei *llb.ExecInfo) {
		var sharing llb.CacheMountSharingMode
		switch c.Sharing {
		case cacheMountShared, cacheMountUnset:
			sharing = llb.CacheMountShared
		case cacheMountLocked:
			sharing = llb.CacheMountLocked
		case cacheMountPrivate:
			sharing = llb.CacheMountPrivate
		default:
			// validation needs to happen before this point
			// if we got here then this is a bug
			panic("invalid cache sharing mode")
		}

		key := c.Key
		if key == "" {
			// No key is set, so use the destination as the key.
			key = c.Dest
		}

		var info CacheDirInfo
		for _, opt := range opts {
			opt.SetCacheDirOption(&info)
		}

		if !c.NoAutoNamespace {
			platform := ei.Platform

			if platform == nil {
				platform = info.Platform
			}

			if platform == nil {
				p := platforms.DefaultSpec()
				platform = &p
			}
			key = fmt.Sprintf("%s-%s-%s", distroKey, platforms.Format(*platform), key)
		}

		llb.AddMount(c.Dest, llb.Scratch(), llb.AsPersistentCacheDir(key, sharing)).SetRunOption(ei)
	})
}

func (c *CacheDir) validate() error {
	var errs []error

	if c.Dest == "" {
		errs = append(errs, fmt.Errorf("cache dir dest is required"))
	}

	if !filepath.IsAbs(c.Dest) {
		errs = append(errs, fmt.Errorf("cache dir dest must be an absolute path: %s", c.Dest))
	}

	switch c.Sharing {
	case cacheMountShared, cacheMountLocked, cacheMountPrivate, cacheMountUnset:
	default:
		errs = append(errs, fmt.Errorf("invalid cache dir sharing mode: %s, valid values: %v", c.Sharing, []string{cacheMountShared, cacheMountLocked, cacheMountPrivate}))
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// GoBuildCache is a cache for Go build artifacts.
// It is used to speed up Go builds by caching the incremental builds.
type GoBuildCache struct {
	// Scope adds extra information to the cache key.
	// This is useful to differentiate between different build contexts if required.
	//
	// This is mainly intended for internal testing purposes.
	Scope string `json:"scope" yaml:"scope"`

	// The gobuild cache may be automatically injected into a build of
	// go is detected.
	// Disabled explicitly turns this off.
	Disabled bool `json:"disabled" yaml:"disabled"`
}

func (c *GoBuildCache) validate() error {
	return nil
}

type GoBuildCacheInfo struct {
	Platform *ocispecs.Platform
}

type GoBuildCacheOption interface {
	SetGoBuildCacheOption(*GoBuildCacheInfo)
}

type GoBuildCacheOptionFunc func(*GoBuildCacheInfo)

func (f GoBuildCacheOptionFunc) SetGoBuildCacheOption(info *GoBuildCacheInfo) {
	f(info)
}

func WithGoCacheConstraints(opts ...llb.ConstraintsOpt) CacheConfigOption {
	return CacheConfigOptionFunc(func(info *CacheInfo) {
		var c llb.Constraints
		for _, opt := range opts {
			opt.SetConstraintsOption(&c)
		}
		info.GoBuild.Platform = c.Platform
	})
}

const goBuildCacheDir = "/tmp/dalec/gobuild-cache"

func (c *GoBuildCache) ToRunOption(distroKey string, opts ...GoBuildCacheOption) llb.RunOption {
	return RunOptFunc(func(ei *llb.ExecInfo) {
		if c.Disabled {
			return
		}

		var info GoBuildCacheInfo
		for _, opt := range opts {
			opt.SetGoBuildCacheOption(&info)
		}

		platform := ei.Platform

		if platform == nil {
			platform = info.Platform
		}
		if platform == nil {
			p := platforms.DefaultSpec()
			platform = &p
		}

		key := fmt.Sprintf("%s-%s-dalec-gobuildcache", distroKey, platforms.Format(*platform))
		if c.Scope != "" {
			key = fmt.Sprintf("%s-%s", key, c.Scope)
		}
		llb.AddMount(goBuildCacheDir, llb.Scratch(), llb.AsPersistentCacheDir(key, llb.CacheMountShared)).SetRunOption(ei)
		llb.AddEnv("GOCACHE", goBuildCacheDir).SetRunOption(ei)
	})
}
