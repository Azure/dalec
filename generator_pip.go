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

func (p *GeneratorPip) flags(withReq bool) string {
	var cmd string
	if p.IndexUrl != "" {
		cmd += " --index-url=" + p.IndexUrl
	}

	for _, extraUrl := range p.ExtraIndexUrls {
		cmd += " --extra-index-url=" + extraUrl
	}

	const defaultReqFile = "requirements.txt"
	requirementsFile := p.RequirementsFile
	if requirementsFile == "" {
		requirementsFile = defaultReqFile
	}

	if withReq {
		cmd += " --requirement=" + requirementsFile
	}

	return cmd
}

func withPip(g *SourceGenerator, worker llb.State, name string, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		paths := g.Pip.Paths
		if g.Pip.Paths == nil {
			paths = []string{"."}
		}

		pkgs := make([]llb.State, 0, len(paths))

		for _, path := range paths {
			base := filepath.Join("/work", name, g.Subpath, path)
			pkgPath := filepath.Join(base, "site-packages")

			downloaded := worker.Run(
				ShArgs("python3 -m pip download --no-binary=:all: --dest="+pkgPath+g.Pip.flags(true)),
				llb.Dir(base),
				WithConstraints(opts...),
			).AddMount("/work", in)

			// TODO: I think this is not right since this will build binaries for the host system
			// but we only want source code here.
			st := worker.Run(
				ShArgs("python3 -m pip install --no-index --find-links="+pkgPath+" --no-deps --target="+pkgPath+g.Pip.flags(true)),
				llb.Dir(base),
				WithConstraints(opts...),
			).AddMount("/work", downloaded)

			pkgs = append(pkgs, st)
		}

		return MergeAtPath(llb.Scratch(), pkgs, "/")
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
			merged = merged.With(withPip(gen, worker, key, opts...))
		}
		result[key] = merged
	}
	return result, nil
}
