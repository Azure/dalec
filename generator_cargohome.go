package dalec

import (
	"path/filepath"

	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"
)

const (
	// CargoCacheKey is the key used to identify the cargo registry cache in buildkit cache.
	CargoCacheKey = "dalec-cargo-registry-cache"
)

func (s *Source) isCargohome() bool {
	for _, gen := range s.Generate {
		if gen.Cargohome != nil {
			return true
		}
	}
	return false
}

// HasCargohomes returns true if any of the sources in the spec are a Rust Cargo project.
func (s *Spec) HasCargohomes() bool {
	for _, src := range s.Sources {
		if src.isCargohome() {
			return true
		}
	}
	return false
}

func withCargohome(g *SourceGenerator, srcSt, worker llb.State, subPath string, opts ...llb.ConstraintsOpt) func(llb.State) llb.State {
	return func(in llb.State) llb.State {
		workDir := "/work/src"
		joinedWorkDir := filepath.Join(workDir, subPath, g.Subpath)
		srcMount := llb.AddMount(workDir, srcSt)

		const (
			registryPath = "/tmp/dalec/cargo-registry-cache"
			sccachePath  = "/tmp/dalec/sccache-binary-cache"
		)

		// First, install sccache binary to our persistent cache (we have network access here)
		sccacheInstallScript := `#!/bin/bash
set -euo pipefail

# Check if sccache is already cached
if [ -f "` + sccachePath + `/sccache" ]; then
    echo "sccache already cached"
    exit 0
fi

# Create cache directory
mkdir -p "` + sccachePath + `"

# Download precompiled sccache binary
ARCH=$(uname -m)
case "$ARCH" in
    x86_64) 
        SCCACHE_ARCH="` + SccacheArchLinuxX64 + `"
        SCCACHE_CHECKSUM="` + SccacheChecksumLinuxX64 + `"
        ;;
    aarch64) 
        SCCACHE_ARCH="` + SccacheArchLinuxArm64 + `"
        SCCACHE_CHECKSUM="` + SccacheChecksumLinuxArm64 + `"
        ;;
    *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

SCCACHE_VERSION="` + SccacheVersion + `"
SCCACHE_URL="` + SccacheDownloadURL + `/${SCCACHE_VERSION}/sccache-${SCCACHE_VERSION}-${SCCACHE_ARCH}.tar.gz"

echo "Downloading sccache ${SCCACHE_VERSION} for ${SCCACHE_ARCH}..."
curl -L "${SCCACHE_URL}" -o /tmp/sccache.tar.gz

# Verify checksum
echo "Verifying checksum..."
if command -v sha256sum >/dev/null 2>&1; then
    echo "${SCCACHE_CHECKSUM}  /tmp/sccache.tar.gz" | sha256sum -c - || {
        echo "ERROR: Checksum verification failed for sccache binary"
        rm -f /tmp/sccache.tar.gz
        exit 1
    }
elif command -v shasum >/dev/null 2>&1; then
    echo "${SCCACHE_CHECKSUM}  /tmp/sccache.tar.gz" | shasum -a 256 -c - || {
        echo "ERROR: Checksum verification failed for sccache binary"
        rm -f /tmp/sccache.tar.gz
        exit 1
    }
else
    echo "WARNING: No checksum utility found (sha256sum or shasum), skipping verification"
fi

# Extract and install
tar xz --strip-components=1 -C "` + sccachePath + `" -f /tmp/sccache.tar.gz
chmod +x "` + sccachePath + `/sccache"
rm -f /tmp/sccache.tar.gz
echo "sccache cached successfully"
`)
		}

		srcMount := llb.AddMount(workDir, srcSt)

		// Get sccache using SourceHTTP instead of shell scripts
		platform := constraints.Platform
		if platform == nil {
			p := platforms.DefaultSpec()
			platform = &p
		}

		sccacheState, err := GetSccacheState(platform, opts...)
		var deps llb.State
		if err != nil {
			// Since we can't return error from this nested function, we'll panic
			// to fail fast if sccache download fails. This could indicate network,
			// authentication, or other infrastructure issues that might affect the build
			panic(errors.Wrap(err, "failed to download sccache via SourceHTTP"))
		}

		// Extract and install sccache using SourceHTTP
		var extractCmd string
		if isWindows {
			extractCmd = `powershell -Command "` +
				`$ErrorActionPreference = 'Stop'; ` +
				`if (Test-Path 'C:\sccache-download\sccache-*.tar.gz') { ` +
				`tar -xzf (Get-ChildItem 'C:\sccache-download\sccache-*.tar.gz')[0].FullName -C 'C:\sccache-download'; ` +
				`$sccacheExe = Get-ChildItem -Path 'C:\sccache-download' -Name 'sccache.exe' -Recurse | Select-Object -First 1; ` +
				`if ($sccacheExe) { ` +
				`New-Item -Path '` + sccachePath + `' -ItemType Directory -Force; ` +
				`Copy-Item $sccacheExe.FullName '` + sccachePath + `\sccache.exe' -Force; ` +
				`Write-Host 'sccache binary installed successfully via SourceHTTP'; ` +
				`} else { ` +
				`Write-Host 'Warning: sccache.exe not found in SourceHTTP archive'; ` +
				`} ` +
				`} else { ` +
				`Write-Host 'Warning: sccache archive not found in SourceHTTP mount'; ` +
				`}"`
		} else {
			extractCmd = `set -euo pipefail; ` +
				`echo "Installing sccache via SourceHTTP..."; ` +
				`if [ -f /sccache-download/sccache-*.tar.gz ]; then ` +
				`mkdir -p "` + sccachePath + `"; ` +
				`tar -xzf /sccache-download/sccache-*.tar.gz -C /sccache-download --strip-components=1; ` +
				`if [ -f /sccache-download/sccache ]; then ` +
				`cp /sccache-download/sccache "` + sccachePath + `/sccache"; ` +
				`chmod +x "` + sccachePath + `/sccache"; ` +
				`echo "sccache binary installed successfully via SourceHTTP"; ` +
				`else ` +
				`echo "Warning: sccache binary not found in SourceHTTP archive"; ` +
				`fi; ` +
				`else ` +
				`echo "Warning: sccache archive not found in SourceHTTP mount"; ` +
				`fi`
		}

		// Install sccache using SourceHTTP mount and extraction
		var mountPath string
		if isWindows {
			mountPath = "C:\\sccache-download"
		} else {
			mountPath = "/sccache-download"
		}

		deps = worker.Run(
			ShArgs(extractCmd),
			llb.AddMount(mountPath, sccacheState, llb.Readonly),
			llb.AddMount(sccachePath, llb.Scratch(), llb.AsPersistentCacheDir(SccacheCacheKey, llb.CacheMountShared)),
			WithConstraints(opts...),
		).Root()

		paths := g.Cargohome.Paths
		if g.Cargohome.Paths == nil {
			paths = []string{"."}
		}

		// Cargo fetch command varies by platform
		var cargoFetchCommand string
		var outputMount string
		if isWindows {
			cargoFetchCommand = `set CARGO_HOME=C:\output && cargo fetch`
			outputMount = "C:\\output"
		} else {
			cargoFetchCommand = `set -e; CARGO_HOME="/output" cargo fetch`
			outputMount = "/output"
		}

		for _, path := range paths {
			// Create a temporary state to capture cargo output
			cargoOutput := worker.Run(
				// Download cargo dependencies and create proper cargo home structure
				ShArgs(cargoFetchCommand),
				llb.Dir(filepath.Join(joinedWorkDir, path)),
				srcMount,
				llb.AddMount(outputMount, llb.Scratch()),
				WithConstraints(opts...),
			).GetMount(outputMount)

			// Copy the cargo registry to the deps state
			var registrySourcePath string
			if isWindows {
				registrySourcePath = "\\registry"
			} else {
				registrySourcePath = "/registry"
			}
			deps = deps.File(llb.Copy(cargoOutput, registrySourcePath, "/registry"))
		}

		return deps
	}
}

func (s *Spec) cargohomeSources() map[string]Source {
	sources := map[string]Source{}
	for name, src := range s.Sources {
		if src.isCargohome() {
			sources[name] = src
		}
	}
	return sources
}

// CargohomeDeps returns an [llb.State] containing all the Cargo dependencies for the spec
// for any sources that have a cargohome generator specified.
// If there are no sources with a cargohome generator, this will return a nil state.
func (s *Spec) CargohomeDeps(sOpt SourceOpts, worker llb.State, opts ...llb.ConstraintsOpt) (*llb.State, error) {
	sources := s.cargohomeSources()
	if len(sources) == 0 {
		return nil, nil
	}

	deps := llb.Scratch()

	// Get the patched sources for the Cargo projects
	// This is needed in case a patch includes changes to Cargo.toml or Cargo.lock
	patched, err := s.getPatchedSources(sOpt, worker, func(name string) bool {
		_, ok := sources[name]
		return ok
	}, opts...)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get patched sources")
	}

	sorted := SortMapKeys(patched)

	for _, key := range sorted {
		src := s.Sources[key]

		opts := append(opts, ProgressGroup("Fetch Cargo dependencies for source: "+key))
		deps = deps.With(func(in llb.State) llb.State {
			for _, gen := range src.Generate {
				if gen.Cargohome != nil {
					in = in.With(withCargohome(gen, patched[key], worker, key, opts...))
				}
			}
			return in
		})
	}

	return &deps, nil
}
