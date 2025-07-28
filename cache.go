package dalec

import (
	"errors"
	"fmt"
	"os"
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

	BazelDefaultSocketID = "bazel-default" // Default ID for bazel socket
)

// CacheConfig configures a cache to use for a build.
type CacheConfig struct {
	// Dir specifies a generic cache directory configuration.
	Dir *CacheDir `json:"dir,omitempty" yaml:"dir,omitempty" jsonschema:"oneof_required=dir"`
	// GoBuild specifies a cache for Go's incremental build artifacts.
	// This should speed up repeated builds of Go projects.
	GoBuild *GoBuildCache `json:"gobuild,omitempty" yaml:"gobuild,omitempty" jsonschema:"oneof_required=gobuild"`
	// CargoBuild specifies a cache for Rust/Cargo build artifacts.
	// This uses sccache to cache Rust compilation artifacts.
	CargoBuild *CargoBuildCache `json:"cargobuild,omitempty" yaml:"cargobuild,omitempty" jsonschema:"oneof_required=cargobuild"`
	// Bazel specifies a cache for bazel builds.
	Bazel *BazelCache `json:"bazel,omitempty" yaml:"bazel,omitempty" jsonschema:"oneof_required=bazel-local"`
}

type CacheInfo struct {
	DirInfo    CacheDirInfo
	GoBuild    GoBuildCacheInfo
	CargoBuild CargoBuildCacheInfo
	Bazel      BazelCacheInfo
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

func (c *CacheConfig) ToRunOption(worker llb.State, distroKey string, opts ...CacheConfigOption) llb.RunOption {
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

	if c.CargoBuild != nil {
		return c.CargoBuild.ToRunOption(worker, distroKey, CargoBuildCacheOptionFunc(func(info *CargoBuildCacheInfo) {
			var cacheInfo CacheInfo
			for _, opt := range opts {
				opt.SetCacheConfigOption(&cacheInfo)
			}
			*info = cacheInfo.CargoBuild
		}))
	}

	if c.Bazel != nil {
		return c.Bazel.ToRunOption(worker, distroKey, BazelCacheOptionFunc(func(info *BazelCacheInfo) {
			var cacheInfo CacheInfo
			for _, opt := range opts {
				opt.SetCacheConfigOption(&cacheInfo)
			}
			*info = cacheInfo.Bazel
		}))
	}

	// Should not reach this point
	panic("invalid cache config")
}

func (c *CacheConfig) validate() error {
	if c == nil {
		return nil
	}

	var count int
	if c.Dir != nil {
		count++
	}
	if c.GoBuild != nil {
		count++
	}
	if c.CargoBuild != nil {
		count++
	}
	if c.Bazel != nil {
		count++
	}

	if count != 1 {
		return fmt.Errorf("invalid cache config: exactly one of (dir, gobuild, cargobuild, bazel) must be set")
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
	if c.CargoBuild != nil {
		if err := c.CargoBuild.validate(); err != nil {
			errs = append(errs, fmt.Errorf("invalid cargo build cache config: %w", err))
		}
	}
	if c.Bazel != nil {
		if err := c.Bazel.validate(); err != nil {
			errs = append(errs, fmt.Errorf("invalid bazel cache config: %w", err))
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
	Scope string `json:"scope,omitempty" yaml:"scope,omitempty"`

	// The gobuild cache may be automatically injected into a build if
	// go is detected.
	// Disabled explicitly turns this off.
	Disabled bool `json:"disabled,omitempty" yaml:"disabled,omitempty"`
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

// CargoBuildCache is a cache for Rust/Cargo build artifacts.
// It uses sccache to speed up Rust compilation by caching build artifacts.
type CargoBuildCache struct {
	// Scope adds extra information to the cache key.
	// This is useful to differentiate between different build contexts if required.
	//
	// This is mainly intended for internal testing purposes.
	Scope string `json:"scope,omitempty" yaml:"scope,omitempty"`

	// The cargobuild cache may be automatically injected into a build if
	// rust is detected.
	// Disabled explicitly turns this off.
	Disabled bool `json:"disabled,omitempty" yaml:"disabled,omitempty"`
}

func (c *CargoBuildCache) validate() error {
	return nil
}

type CargoBuildCacheInfo struct {
	Platform *ocispecs.Platform
}

type CargoBuildCacheOption interface {
	SetCargoBuildCacheOption(*CargoBuildCacheInfo)
}

type CargoBuildCacheOptionFunc func(*CargoBuildCacheInfo)

func (f CargoBuildCacheOptionFunc) SetCargoBuildCacheOption(info *CargoBuildCacheInfo) {
	f(info)
}

func WithCargoCacheConstraints(opts ...llb.ConstraintsOpt) CacheConfigOption {
	return CacheConfigOptionFunc(func(info *CacheInfo) {
		var c llb.Constraints
		for _, opt := range opts {
			opt.SetConstraintsOption(&c)
		}
		info.CargoBuild.Platform = c.Platform
	})
}

const (
	sccacheCacheDir    = "/tmp/dalec/sccache-cache"
	sccacheBinary      = "/tmp/dalec/sccache"
	sccacheCacheDirWin = "C:\\temp\\dalec\\sccache-cache"
	sccacheBinaryWin   = "C:\\temp\\dalec\\sccache.exe"
	// SccacheCacheKey is the key used to identify the sccache binary cache in buildkit cache.
	// This must match the key used in generator_cargohome.go
	SccacheCacheKey = "dalec-sccache-binary-cache"
)

func (c *CargoBuildCache) ToRunOption(worker llb.State, distroKey string, opts ...CargoBuildCacheOption) llb.RunOption {
	return RunOptFunc(func(ei *llb.ExecInfo) {
		if c.Disabled {
			return
		}

		var info CargoBuildCacheInfo
		for _, opt := range opts {
			opt.SetCargoBuildCacheOption(&info)
		}

		platform := ei.Platform

		if platform == nil {
			platform = info.Platform
		}
		if platform == nil {
			p := platforms.DefaultSpec()
			platform = &p
		}

		key := fmt.Sprintf("%s-%s-dalec-cargobuildcache", distroKey, platforms.Format(*platform))
		if c.Scope != "" {
			key = fmt.Sprintf("%s-%s", key, c.Scope)
		}

		// Determine paths and configuration based on platform
		isWindows := c.isWindowsPlatform(distroKey)

		var (
			cacheDir, binaryPath, scriptPath, scriptName       string
			setupScriptName, sccacheFromCargoCache, setupMount string
			fileMode                                           os.FileMode
			setupScript                                        string
		)

		if isWindows {
			cacheDir = sccacheCacheDirWin
			binaryPath = sccacheBinaryWin
			scriptPath = "C:\\temp\\dalec\\scripts"
			scriptName = "install_sccache.ps1"
			setupScriptName = "setup_sccache.ps1"
			sccacheFromCargoCache = "C:\\temp\\dalec\\sccache-binary-cache\\sccache.exe"
			setupMount = "C:\\temp\\dalec\\setup"
			fileMode = 0o644 // PowerShell scripts don't need execute bit on Windows

			setupScript = `# PowerShell setup script
if (Test-Path "` + sccacheFromCargoCache + `") {
    Write-Host "Using pre-installed sccache from cargo cache"
    Copy-Item "` + sccacheFromCargoCache + `" "` + binaryPath + `" -Force
} else {
    Write-Host "Installing sccache using fallback method"
    & "` + scriptPath + `\` + scriptName + `"
}
`
		} else {
			cacheDir = sccacheCacheDir
			binaryPath = sccacheBinary
			scriptPath = "/tmp/dalec/scripts"
			scriptName = "install_sccache.sh"
			setupScriptName = "setup_sccache.sh"
			sccacheFromCargoCache = "/tmp/dalec/sccache-binary-cache/sccache"
			setupMount = "/tmp/dalec/setup"
			fileMode = 0o755

			setupScript = `#!/bin/bash
set -e
# Check if we have sccache from the cargo dependency cache
if [ -f "` + sccacheFromCargoCache + `" ]; then
    echo "Using pre-installed sccache from cargo cache"
    cp "` + sccacheFromCargoCache + `" "` + binaryPath + `"
    chmod +x "` + binaryPath + `"
else
    echo "Installing sccache using fallback method"
    # Run the installation script
    ` + scriptPath + `/` + scriptName + `
fi
`
		}

		setupScriptSt := llb.Scratch().File(llb.Mkfile(setupScriptName, 0o755, []byte(setupScript)))
		installSccache := c.installSccacheScript(distroKey)

		// Set up cache mounts and environment
		llb.AddMount(cacheDir, llb.Scratch(), llb.AsPersistentCacheDir(key, llb.CacheMountShared)).SetRunOption(ei)

		// Mount the sccache binary cache to check for pre-installed sccache
		sccacheBinaryCacheMount := "/tmp/dalec/sccache-binary-cache"
		if isWindows {
			sccacheBinaryCacheMount = "C:\\temp\\dalec\\sccache-binary-cache"
		}
		llb.AddMount(sccacheBinaryCacheMount, llb.Scratch(), llb.AsPersistentCacheDir(SccacheCacheKey, llb.CacheMountShared)).SetRunOption(ei)

		llb.AddEnv("SCCACHE_DIR", cacheDir).SetRunOption(ei)
		llb.AddEnv("SCCACHE_CACHE_SIZE", "10G").SetRunOption(ei)
		llb.AddEnv("RUSTC_WRAPPER", binaryPath).SetRunOption(ei)

		// Add both the setup script and the fallback installation script
		llb.AddMount(setupMount, setupScriptSt).SetRunOption(ei)

		sccacheScript := llb.Scratch().File(llb.Mkfile(scriptName, fileMode, installSccache))
		llb.AddMount(scriptPath, sccacheScript).SetRunOption(ei)

		// Run the setup script that checks for pre-installed sccache first
		if isWindows {
			llb.Shlex("powershell -ExecutionPolicy Bypass -File " + setupMount + "\\" + setupScriptName).SetRunOption(ei)
		} else {
			llb.Shlex("bash " + setupMount + "/" + setupScriptName).SetRunOption(ei)
		}
	})
}

// installSccacheScript generates a script to install sccache
func (c *CargoBuildCache) installSccacheScript(distroKey string) []byte {
	// Check if this is a Windows platform
	if c.isWindowsPlatform(distroKey) {
		return c.generateWindowsSccacheScript()
	}

	// List of distros that need precompiled binaries
	needsPrecompiled := map[string]bool{
		"almalinux8":  true,
		"almalinux9":  true,
		"rockylinux8": true,
		"rockylinux9": true,
		"bullseye":    true,
		"bionic":      true,
		"focal":       true,
		"jammy":       true,
	}

	script := `#!/bin/bash
set -euo pipefail

# Check if sccache is already installed
if command -v sccache >/dev/null 2>&1; then
    ln -sf "$(command -v sccache)" "` + sccacheBinary + `"
    exit 0
fi

`

	if needsPrecompiled[distroKey] {
		script += `# Download precompiled sccache binary for distros without package
ARCH=$(uname -m)
case "$ARCH" in
    x86_64) SCCACHE_ARCH="x86_64-unknown-linux-musl" ;;
    aarch64) SCCACHE_ARCH="aarch64-unknown-linux-musl" ;;
    *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

SCCACHE_VERSION="v0.10.0"
SCCACHE_URL="https://github.com/mozilla/sccache/releases/download/${SCCACHE_VERSION}/sccache-${SCCACHE_VERSION}-${SCCACHE_ARCH}.tar.gz"

echo "Downloading sccache ${SCCACHE_VERSION} for ${SCCACHE_ARCH}..."
curl -L "${SCCACHE_URL}" | tar xz --strip-components=1 -C /tmp
mv /tmp/sccache "` + sccacheBinary + `"
chmod +x "` + sccacheBinary + `"
`
	} else {
		script += `# Install sccache from package manager
if command -v apt-get >/dev/null 2>&1; then
    apt-get update && apt-get install -y sccache
    ln -sf "$(command -v sccache)" "` + sccacheBinary + `"
elif command -v dnf >/dev/null 2>&1; then
    dnf install -y sccache
    ln -sf "$(command -v sccache)" "` + sccacheBinary + `"
elif command -v tdnf >/dev/null 2>&1; then
    tdnf install -y sccache
    ln -sf "$(command -v sccache)" "` + sccacheBinary + `"
else
    echo "No supported package manager found"
    exit 1
fi
`
	}

	return []byte(script)
}

// isWindowsPlatform checks if the distro key indicates a Windows platform
func (c *CargoBuildCache) isWindowsPlatform(distroKey string) bool {
	windowsPlatforms := map[string]bool{
		"windowsservercore": true,
		"nanoserver":        true,
		"windows":           true,
	}
	return windowsPlatforms[distroKey]
}

// generateWindowsSccacheScript creates a PowerShell script for Windows
func (c *CargoBuildCache) generateWindowsSccacheScript() []byte {
	const windowsSccacheBinary = "C:\\temp\\dalec\\sccache.exe"

	script := `# PowerShell script to install sccache on Windows
$ErrorActionPreference = "Stop"

# Check if sccache is already installed
$existingSccache = Get-Command sccache -ErrorAction SilentlyContinue
if ($existingSccache) {
    # Create symlink or copy to our expected location
    New-Item -Path "C:\temp\dalec" -ItemType Directory -Force | Out-Null
    Copy-Item -Path $existingSccache.Source -Destination "` + windowsSccacheBinary + `" -Force
    exit 0
}

# Create temp directory
New-Item -Path "C:\temp\dalec" -ItemType Directory -Force | Out-Null

# Detect architecture
$arch = if ([Environment]::Is64BitOperatingSystem) { "x86_64" } else { "i686" }
$sccacheArch = "$arch-pc-windows-msvc"

$sccacheVersion = "v0.7.4"
$sccacheUrl = "https://github.com/mozilla/sccache/releases/download/$sccacheVersion/sccache-$sccacheVersion-$sccacheArch.zip"

Write-Host "Downloading sccache $sccacheVersion for $sccacheArch..."

# Download and extract sccache
$tempZip = "C:\temp\dalec\sccache.zip"
try {
    Invoke-WebRequest -Uri $sccacheUrl -OutFile $tempZip -UseBasicParsing
    Expand-Archive -Path $tempZip -DestinationPath "C:\temp\dalec\sccache-extract" -Force
    
    # Find and move the sccache.exe binary
    $sccacheExe = Get-ChildItem -Path "C:\temp\dalec\sccache-extract" -Name "sccache.exe" -Recurse | Select-Object -First 1
    if ($sccacheExe) {
        $sourcePath = Join-Path "C:\temp\dalec\sccache-extract" $sccacheExe.DirectoryName "sccache.exe"
        Move-Item -Path $sourcePath -Destination "` + windowsSccacheBinary + `" -Force
    } else {
        throw "sccache.exe not found in downloaded archive"
    }
    
    Write-Host "sccache installed successfully to ` + windowsSccacheBinary + `"
} finally {
    # Clean up temporary files
    Remove-Item -Path $tempZip -Force -ErrorAction SilentlyContinue
    Remove-Item -Path "C:\temp\dalec\sccache-extract" -Recurse -Force -ErrorAction SilentlyContinue
}
`

	return []byte(script)
}

// BazelCache sets up a cache for bazel builds.
//
// Currently this only supports setting up a *local* bazel cache.
//
// BazelCache relies on the *system* bazelrc file to configure the default cache location.
// If the project being built includes its own bazelrc it may override the one configured by BazelCache.
//
// An alternative to BazelCache would be a [CacheDir] and use `--disk_cache` to set the cache location
// when executing bazel commands.
type BazelCache struct {
	// Scope adds extra information to the cache key.
	// This is useful to differentiate between different build contexts if required.
	//
	// This is mainly intended for internal testing purposes.
	Scope string `json:"scope,omitempty" yaml:"scope,omitepty"`
}

func (c *BazelCache) validate() error {
	return nil
}

type BazelCacheInfo struct {
	Platform    *ocispecs.Platform
	constraints *llb.Constraints
}

func WithBazelCacheConstraints(opts ...llb.ConstraintsOpt) CacheConfigOption {
	return CacheConfigOptionFunc(func(info *CacheInfo) {
		var c llb.Constraints
		for _, opt := range opts {
			opt.SetConstraintsOption(&c)
		}
		info.Bazel.Platform = c.Platform
		info.Bazel.constraints = &c
	})
}

type BazelCacheOptionFunc func(*BazelCacheInfo)

func (f BazelCacheOptionFunc) SetBazelCacheOption(info *BazelCacheInfo) {
	f(info)
}

type BazelCacheOption interface {
	SetBazelCacheOption(*BazelCacheInfo)
}

func (c *BazelCache) ToRunOption(worker llb.State, distroKey string, opts ...BazelCacheOption) llb.RunOption {
	return RunOptFunc(func(ei *llb.ExecInfo) {
		var info BazelCacheInfo

		for _, opt := range opts {
			opt.SetBazelCacheOption(&info)
		}

		platform := ei.Platform

		if platform == nil {
			platform = info.Platform
		}
		if platform == nil {
			p := platforms.DefaultSpec()
			platform = &p
		}

		key := fmt.Sprintf("%s-%s-dalec-bazelcache", distroKey, platforms.Format(*platform))
		if c.Scope != "" {
			key = fmt.Sprintf("%s-%s", key, c.Scope)
		}

		// See bazelrc https://bazel.build/run/bazelrc for more information on the bazelrc file

		const (
			cacheDir = "/tmp/dalec/bazel-local-cache"
			sockPath = "/tmp/dalec/bazel-remote.sock"
		)

		rcFileContent := `
build --disk_cache=` + cacheDir + `
fetch --disk_cache=` + cacheDir + `
`
		rcFile := llb.Scratch().File(
			llb.Mkfile("bazelrc", 0o644, []byte(rcFileContent)),
			WithConstraint(info.constraints),
		)

		checkSockPath := filepath.Join(filepath.Dir(sockPath), c.Scope, filepath.Base(sockPath))
		checkScript := fmt.Sprintf(`#!/usr/bin/env sh

if [ -S %q ]; then
	echo "build --remote_cache=unix:%s" >> /tmp/dalec/bazelrc
	echo "fetch --remote_cache=unix:%s" >> /tmp/dalec/bazelrc
fi
`, checkSockPath, sockPath, sockPath)
		checkScriptSt := llb.Scratch().File(
			llb.Mkfile("script.sh", 0o755, []byte(checkScript)),
			WithConstraint(info.constraints),
		)

		scriptPath := "/tmp/dalec/internal/bazel/check-socket.sh"
		rcFile = worker.Run(
			llb.AddSSHSocket(llb.SSHID(BazelDefaultSocketID), llb.SSHSocketTarget(checkSockPath), llb.SSHOptional),
			ShArgs(scriptPath),
			llb.AddMount(scriptPath, checkScriptSt, llb.SourcePath("script.sh")),
			WithConstraint(info.constraints),
		).AddMount("/tmp/dalec/bazelrc", rcFile, llb.SourcePath("bazelrc"))

		llb.AddMount("/etc/bazel.bazelrc", rcFile, llb.SourcePath("bazelrc")).SetRunOption(ei)
		llb.AddMount(cacheDir, llb.Scratch(), llb.AsPersistentCacheDir(key, llb.CacheMountShared)).SetRunOption(ei)
		llb.AddSSHSocket(llb.SSHID(BazelDefaultSocketID), llb.SSHSocketTarget(sockPath), llb.SSHOptional).SetRunOption(ei)
	})
}
