package dalec

import (
	"path/filepath"

	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"
)

const (
	cargoHomeDir = "/cargo/registry"
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
    x86_64) SCCACHE_ARCH="x86_64-unknown-linux-musl" ;;
    aarch64) SCCACHE_ARCH="aarch64-unknown-linux-musl" ;;
    *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

SCCACHE_VERSION="v0.10.0"
SCCACHE_URL="https://github.com/mozilla/sccache/releases/download/${SCCACHE_VERSION}/sccache-${SCCACHE_VERSION}-${SCCACHE_ARCH}.tar.gz"

echo "Downloading sccache ${SCCACHE_VERSION} for ${SCCACHE_ARCH}..."
curl -L "${SCCACHE_URL}" | tar xz --strip-components=1 -C "` + sccachePath + `"
chmod +x "` + sccachePath + `/sccache"
echo "sccache cached successfully"
`

		sccacheScript := llb.Scratch().File(llb.Mkfile("install_sccache.sh", 0o755, []byte(sccacheInstallScript)))

		// Install sccache to persistent cache
		deps := worker.Run(
			ShArgs("bash /tmp/install_sccache.sh"),
			llb.AddMount("/tmp", sccacheScript),
			llb.AddMount(sccachePath, llb.Scratch(), llb.AsPersistentCacheDir(SccacheCacheKey, llb.CacheMountShared)),
			llb.Network(llb.NetModeSandbox), // Enable network for sccache download
			WithConstraints(opts...),
		).Root()

		paths := g.Cargohome.Paths
		if g.Cargohome.Paths == nil {
			paths = []string{"."}
		}

		for _, path := range paths {
			deps = worker.Run(
				// Download cargo dependencies - let cargo create the proper registry structure
				ShArgs(`set -e; CARGO_HOME="/output" cargo fetch`),
				llb.Dir(filepath.Join(joinedWorkDir, path)),
				srcMount,
				WithConstraints(opts...),
			).Root()
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
