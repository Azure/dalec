package dalec

import (
	"path/filepath"

	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"
)

const (
	pipCacheDir = "/pip/cache"
	PipCacheKey = "dalec-pip-cache"
)

func (s *Source) isPip() bool {
	for _, gen := range s.Generate {
		if gen.Pip != nil {
			return true
		}
	}
	return false
}

func (s *Spec) HasPips() bool {
	for _, src := range s.Sources {
		if src.isPip() {
			return true
		}
	}
	return false
}

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
			requirementsFile := g.Pip.RequirementsFile
			if requirementsFile == "" {
				requirementsFile = "requirements.txt"
			}

			pipCmd := "set -e; "

			// Always use --no-binary=:all: to force source builds for architecture independence
			// Use explicit --cache-dir to avoid conflicts with user's PIP_CACHE_DIR
			// Use --break-system-packages to bypass PEP 668 externally-managed-environment protection
			// Use --ignore-installed to avoid conflicts with system packages
			pipCmd += "pip install --no-binary=:all: --cache-dir=" + pipProxyPath + " --break-system-packages --ignore-installed "

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

func (s *Spec) pipSources() map[string]Source {
	sources := map[string]Source{}
	for name, src := range s.Sources {
		if src.isPip() {
			sources[name] = src
		}
	}
	return sources
}

// PipDeps returns an [llb.State] containing all the pip dependencies for the spec
// for any sources that have a pip generator specified.
// It fetches the patched sources and applies the pip generators to them.
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
