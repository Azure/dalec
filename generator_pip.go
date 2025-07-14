package dalec

import (
	"path/filepath"

	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"
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

func withPip(g *SourceGenerator, srcSt, worker llb.State, opts ...llb.ConstraintsOpt) llb.State {
	workDir := "/work/src"
	joinedWorkDir := filepath.Join(workDir, g.Subpath)
	srcMount := llb.AddMount(workDir, srcSt)

	paths := g.Pip.Paths
	if g.Pip.Paths == nil {
		paths = []string{"."}
	}

	result := srcSt
	for _, path := range paths {
		requirementsFile := g.Pip.RequirementsFile
		if requirementsFile == "" {
			requirementsFile = "requirements.txt"
		}

		pipCmd := "set -e; "

		// Create the site-packages directory within the source
		pipCmd += "mkdir -p site-packages; "

		// First, download essential build dependencies that are needed for source builds
		pipCmd += "python3 -m pip download --dest=/tmp/pip-cache setuptools wheel"

		if g.Pip.IndexUrl != "" {
			pipCmd += " --index-url=" + g.Pip.IndexUrl
		}
		for _, extraUrl := range g.Pip.ExtraIndexUrls {
			pipCmd += " --extra-index-url=" + extraUrl
		}

		pipCmd += "; python3 -m pip download --no-binary=:all: --dest=/tmp/pip-cache --requirement=" + requirementsFile

		// Add custom index URLs for main dependencies if specified
		if g.Pip.IndexUrl != "" {
			pipCmd += " --index-url=" + g.Pip.IndexUrl
		}
		for _, extraUrl := range g.Pip.ExtraIndexUrls {
			pipCmd += " --extra-index-url=" + extraUrl
		}

		// Install packages to site-packages directory within the source
		pipCmd += "; python3 -m pip install --no-deps --target=site-packages --find-links=/tmp/pip-cache --no-index --requirement=" + requirementsFile

		result = worker.Run(
			llb.Args([]string{"bash", "-c", pipCmd}),
			llb.Dir(filepath.Join(joinedWorkDir, path)),
			srcMount,
			WithConstraints(opts...),
		).AddMount(workDir, result)
	}
	return result
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

// PipDeps returns a map[string]llb.State containing all the pip dependencies for the spec
// for any sources that have a pip generator specified.
// If there are no sources with a pip generator, this will return nil.
// The returned states have site-packages installed for each relevant source, using sources as input.
func (s *Spec) PipDeps(sOpt SourceOpts, worker llb.State, opts ...llb.ConstraintsOpt) (map[string]llb.State, error) {
	sources := s.pipSources()
	if len(sources) == 0 {
		return nil, nil
	}

	// Get the patched sources for the Python projects
	// This is needed in case a patch includes changes to requirements.txt
	patched, err := s.getPatchedSources(sOpt, worker, func(name string) bool {
		_, ok := sources[name]
		return ok
	}, opts...)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get patched sources")
	}

	result := make(map[string]llb.State)
	sorted := SortMapKeys(patched)
	opts = append(opts, ProgressGroup("Fetch pip dependencies for sources"))
	for _, key := range sorted {
		src := s.Sources[key]
		merged := patched[key]
		for _, gen := range src.Generate {
			if gen.Pip == nil {
				continue
			}
			merged = withPip(gen, merged, worker, opts...)
		}
		result[key] = merged
	}
	return result, nil
}
