package dalec

import (
	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"
)

const (
	gomodCacheDir = "/go/pkg/mod"
)

func (s *Source) isGomod() bool {
	for _, gen := range s.Generate {
		if gen.Gomod != nil {
			return true
		}
	}
	return false
}

// HasGomods returns true if any of the sources in the spec are a go module.
func (s *Spec) HasGomods() bool {
	for _, src := range s.Sources {
		if src.isGomod() {
			return true
		}
	}
	return false
}

func withGomod(g *SourceGenerator, srcSt, worker llb.State, opts ...llb.ConstraintsOpt) func(llb.State) llb.State {
	return func(in llb.State) llb.State {
		const workDir = "/work/src"
		var srcMount llb.RunOption
		if g.Subpath != "" {
			srcMount = llb.AddMount(workDir, srcSt, llb.SourcePath(g.Subpath))
		} else {
			srcMount = llb.AddMount(workDir, srcSt)
		}
		return worker.Run(
			ShArgs("go mod download"),
			llb.AddEnv("GOMODCACHE", gomodCacheDir),
			llb.Dir(workDir),
			srcMount,
			WithConstraints(opts...),
		).AddMount(gomodCacheDir, in)
	}
}

func (s *Spec) gomodSources() map[string]Source {
	sources := map[string]Source{}
	for name, src := range s.Sources {
		if src.isGomod() {
			sources[name] = src
		}
	}
	return sources
}

// GomodDeps returns an [llb.State] containing all the go module dependencies for the spec
// for any sources that have a gomod generator specified.
// If there are no sources with a gomod generator, this will return a nil state.
func (s *Spec) GomodDeps(sOpt SourceOpts, worker llb.State, opts ...llb.ConstraintsOpt) (*llb.State, error) {
	sources := s.gomodSources()
	if len(sources) == 0 {
		return nil, nil
	}

	deps := llb.Scratch()

	// Get the patched sources for the go modules
	// This is needed in case a patch includes changes to go.mod or go.sum
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

		opts := append(opts, ProgressGroup("Fetch go module dependencies for source: "+key))
		deps = deps.With(func(in llb.State) llb.State {
			for _, gen := range src.Generate {
				in = in.With(withGomod(gen, patched[key], worker, opts...))
			}
			return in
		})
	}

	return &deps, nil
}
