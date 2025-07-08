package dalec

import (
	"path/filepath"

	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"
)

func (s *Source) isNodeMod() bool {
	for _, gen := range s.Generate {
		if gen.NodeMod != nil {
			return true
		}
	}
	return false
}

// HasNodeMods returns true if any of the sources in the spec are node modules.
func (s *Spec) HasNodeMods() bool {
	for _, src := range s.Sources {
		if src.isNodeMod() {
			return true
		}
	}
	return false
}

func withNodeMod(g *SourceGenerator, srcSt, worker llb.State, opts ...llb.ConstraintsOpt) llb.State {
	workDir := "/work/src"
	joinedWorkDir := filepath.Join(workDir, g.Subpath)
	srcMount := llb.AddMount(workDir, srcSt)
	installCmd := "npm install"

	paths := g.NodeMod.Paths
	if g.NodeMod.Paths == nil {
		paths = []string{"."}
	}

	result := srcSt
	for _, path := range paths {
		result = worker.Run(
			ShArgs(installCmd),
			llb.Dir(filepath.Join(joinedWorkDir, path)),
			srcMount,
			WithConstraints(opts...),
		).AddMount(workDir, result)
	}
	return result
}

func (s *Spec) nodeModSources() map[string]Source {
	sources := map[string]Source{}
	for name, src := range s.Sources {
		if src.isNodeMod() {
			sources[name] = src
		}
	}
	return sources
}

// NodeModDeps returns a map[string]llb.State containing all the node module dependencies for the spec
// for any sources that have a node module generator specified.
// If there are no sources with a node module generator, this will return nil.
// The returned states have node_modules installed for each relevant source, using sources as input.
func (s *Spec) NodeModDeps(sOpt SourceOpts, worker llb.State, opts ...llb.ConstraintsOpt) (map[string]llb.State, error) {
	sources := s.nodeModSources()
	if len(sources) == 0 {
		return nil, nil
	}

	// Get the patched sources for the node modules
	patched, err := s.getPatchedSources(sOpt, worker, func(name string) bool {
		_, ok := sources[name]
		return ok
	}, opts...)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get patched sources")
	}

	result := make(map[string]llb.State)
	sorted := SortMapKeys(patched)
	opts = append(opts, ProgressGroup("Fetch node module dependencies for sources"))
	for _, key := range sorted {
		src := s.Sources[key]
		merged := patched[key]
		for _, gen := range src.Generate {
			if gen.NodeMod == nil {
				continue
			}
			merged = withNodeMod(gen, merged, worker, opts...)
		}
		result[key] = merged
	}
	return result, nil
}
