package dalec

import (
	"path/filepath"

	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"
)

// Constants for pip cache directories and keys
const (
	pipCacheDir = "/pip/cache"
	PipCacheKey = "dalec-pip-cache"
)

// Source detection method
func (s *Source) isPip() bool {
	for _, gen := range s.Generate {
		if gen.Pip != nil {
			return true
		}
	}
	return false
}

// Spec-level detection method
func (s *Spec) HasPips() bool {
	for _, src := range s.Sources {
		if src.isPip() {
			return true
		}
	}
	return false
}

// Core generator logic
func withPip(g *SourceGenerator, srcSt, worker llb.State, opts ...llb.ConstraintsOpt) func(llb.State) llb.State {
	return func(in llb.State) llb.State {
		workDir := "/work/src"
		joinedWorkDir := filepath.Join(workDir, g.Subpath)
		srcMount := llb.AddMount(workDir, srcSt)

		paths := g.Pip.Paths
		if g.Pip.Paths == nil {
			paths = []string{"."}
		}

		const pipProxyPath = "/tmp/dalec/pip-proxy-cache"
		for _, path := range paths {
			// Build pip command with appropriate flags
			requirementsFile := g.Pip.RequirementsFile
			if requirementsFile == "" {
				requirementsFile = "requirements.txt"
			}

			// Construct the pip install command
			pipCmd := "set -e; "

			// Always use --no-binary=:all: to force source builds for architecture independence
			// Use explicit --cache-dir to avoid conflicts with user's PIP_CACHE_DIR
			// Use --break-system-packages to bypass PEP 668 externally-managed-environment protection
			pipCmd += "pip install --no-binary=:all: --cache-dir=" + pipProxyPath + " --break-system-packages "

			// Add requirements file
			pipCmd += "--requirement=" + requirementsFile

			// Add custom index URLs if specified
			if g.Pip.IndexUrl != "" {
				pipCmd += " --index-url=" + g.Pip.IndexUrl
			}
			for _, extraUrl := range g.Pip.ExtraIndexUrls {
				pipCmd += " --extra-index-url=" + extraUrl
			}

			in = worker.Run(
				// Download and install dependencies using pip with isolated cache
				ShArgs(pipCmd),
				llb.IgnoreCache,
				llb.Dir(filepath.Join(joinedWorkDir, path)),
				srcMount,
				llb.AddMount(pipProxyPath, llb.Scratch(), llb.AsPersistentCacheDir(PipCacheKey, llb.CacheMountShared)),
				WithConstraints(opts...),
			).AddMount(pipCacheDir, in)
		}
		return in
	}
}

// Source filtering
func (s *Spec) pipSources() map[string]Source {
	sources := map[string]Source{}
	for name, src := range s.Sources {
		if src.isPip() {
			sources[name] = src
		}
	}
	return sources
}

// Main dependency resolution method
func (s *Spec) PipDeps(sOpt SourceOpts, worker llb.State, opts ...llb.ConstraintsOpt) (*llb.State, error) {
	sources := s.pipSources()
	if len(sources) == 0 {
		return nil, nil
	}

	deps := llb.Scratch()

	// Get the patched sources for the Python projects
	// This is needed in case a patch includes changes to requirements.txt
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

		opts := append(opts, ProgressGroup("Fetch pip dependencies for source: "+key))
		deps = deps.With(func(in llb.State) llb.State {
			for _, gen := range src.Generate {
				if gen.Pip != nil {
					in = in.With(withPip(gen, patched[key], worker, opts...))
				}
			}
			return in
		})
	}

	return &deps, nil
}
