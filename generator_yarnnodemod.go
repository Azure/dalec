package dalec

import (
	"path/filepath"

	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"
)

const (
	yarnCacheDir = "/node-mods/yarn-dalec-cache"
	npmCacheDir  = "/node-mods/npm-dalec-cache"
	baseDir      = "/node-mods"
)

func (s *Source) isYarnNodeMod() bool {
	for _, gen := range s.Generate {
		if gen.YarnNodeMod != nil {
			return true
		}
	}
	return false
}

// HasYarnNodeMods returns true if any of the sources in the spec are a Yarn node module.
func (s *Spec) HasYarnNodeMods() bool {
	for _, src := range s.Sources {
		if src.isYarnNodeMod() {
			return true
		}
	}
	return false
}

func withYarnNodeMod(g *SourceGenerator, srcSt, worker llb.State, opts ...llb.ConstraintsOpt) func(llb.State) llb.State {
	return func(in llb.State) llb.State {
		workDir := "/work/src"
		joinedWorkDir := filepath.Join(workDir, g.Subpath)
		srcMount := llb.AddMount(workDir, srcSt)

		in = worker.Run(
			ShArgs("npm install --cache "+npmCacheDir+" -g yarn; yarn config set yarn-offline-mirror "+yarnCacheDir+"; yarn install"),
			llb.Dir(joinedWorkDir),
			srcMount,
			WithConstraints(opts...),
		).AddMount(baseDir, in)

		return in
	}
}

func (s *Spec) yarnNodeModSources() map[string]Source {
	sources := map[string]Source{}
	for name, src := range s.Sources {
		if src.isYarnNodeMod() {
			sources[name] = src
		}
	}
	return sources
}

// YarnNodeModDeps returns an [llb.State] containing all the Yarn node module dependencies for the spec
// for any sources that have a Yarn node module generator specified.
// If there are no sources with a Yarn node module generator, this will return a nil state.
func (s *Spec) YarnNodeModDeps(sOpt SourceOpts, worker llb.State, opts ...llb.ConstraintsOpt) (*llb.State, error) {
	sources := s.yarnNodeModSources()
	if len(sources) == 0 {
		return nil, nil
	}

	deps := llb.Scratch()

	// Get the patched sources for the npm node modules
	// This is needed in case a patch includes changes to package.json or package-lock.json
	patched, err := s.getPatchedSources(sOpt, worker, func(name string) bool {
		_, ok := sources[name]
		return ok
	}, opts...)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get patched sources")
	}

	sorted := SortMapKeys(patched)
	opts = append(opts, ProgressGroup("Fetch npm node module dependencies for source: all before: "))
	for _, key := range sorted {

		src := s.Sources[key]

		deps = deps.With(func(in llb.State) llb.State {
			for _, gen := range src.Generate {
				in = in.With(withYarnNodeMod(gen, patched[key], worker, opts...))
			}
			return in
		})
	}

	return &deps, nil
}
