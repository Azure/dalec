package dalec

import (
	"path/filepath"

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
		// Extract platform information from constraints
		var constraints llb.Constraints
		for _, opt := range opts {
			opt.SetConstraintsOption(&constraints)
		}

		// Determine if this is a Windows platform
		isWindows := constraints.Platform != nil && constraints.Platform.OS == "windows"

		// Set up paths based on platform
		var workDir, joinedWorkDir string

		if isWindows {
			workDir = "C:\\work\\src"
			joinedWorkDir = filepath.Join(workDir, g.Subpath)
		} else {
			workDir = "/work/src"
			joinedWorkDir = filepath.Join(workDir, g.Subpath)
		}

		srcMount := llb.AddMount(workDir, srcSt)

		// Start with worker state as base for dependencies
		deps := worker

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
