package dalec

import (
	"path/filepath"

	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"
)

const (
	pipCacheDir = "/cache"
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

		for _, path := range paths {
			requirementsFile := g.Pip.RequirementsFile
			if requirementsFile == "" {
				requirementsFile = "requirements.txt"
			}

			// Build pip download command to cache dependencies
			pipCmd := "set -e; "
			pipCmd += "python3 -m pip install --upgrade pip; "
			pipCmd += "python3 -m pip download --no-binary=:all: --no-build-isolation"

			// Set cache directory to the mount point
			pipCmd += " --dest=" + pipCacheDir

			// Add requirements file
			pipCmd += " --requirement=" + requirementsFile

			// Add custom index URLs if specified
			if g.Pip.IndexUrl != "" {
				pipCmd += " --index-url=" + g.Pip.IndexUrl
			}
			for _, extraUrl := range g.Pip.ExtraIndexUrls {
				pipCmd += " --extra-index-url=" + extraUrl
			}

			// Also download common build dependencies that are often needed for source builds
			pipCmd += "; python3 -m pip download --no-binary=:all: --no-build-isolation --dest=" + pipCacheDir + " setuptools wheel build"

			// Add debug commands to inspect directory structure
			pipCmd += "; echo '=== DEBUG: Listing contents of " + pipCacheDir + " ===';"
			pipCmd += " ls -la " + pipCacheDir + " || echo 'Directory " + pipCacheDir + " does not exist';"
			pipCmd += " echo '=== DEBUG: Listing contents of /cache ===';"
			pipCmd += " ls -la /cache || echo 'Directory /cache does not exist';"
			pipCmd += " echo '=== DEBUG: Listing contents of root ===';"
			pipCmd += " ls -la / || echo 'Cannot list root directory';"

			in = worker.Run(
				llb.Args([]string{"bash", "-c", pipCmd}),
				llb.Dir(filepath.Join(joinedWorkDir, path)),
				srcMount,
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

		opts := append(opts, ProgressGroup("Download pip dependencies for source: "+key))
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
