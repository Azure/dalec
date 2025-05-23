package dalec

import (
	"path/filepath"

	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"
)

const (
	baseDir      = "/node-mods"
	yarnCacheDir = baseDir + "/yarn-dalec-cache"
	npmCacheDir  = baseDir + "/npm-dalec-cache"
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

func (s *Spec) HasYarnPackageManager() bool {
	for _, src := range s.Sources {
		if src.isNodeMod() {
			for _, gen := range src.Generate {
				if gen.NodeMod != nil && gen.NodeMod.PackageManager == "yarn" {
					return true
				}
			}
		}
	}
	return false
}

func withNodeMod(g *SourceGenerator, srcSt, worker llb.State, opts ...llb.ConstraintsOpt) func(llb.State) llb.State {
	return func(in llb.State) llb.State {
		workDir := "/work/src"
		joinedWorkDir := filepath.Join(workDir, g.Subpath)
		srcMount := llb.AddMount(workDir, srcSt)

		var installCmd string
		if g.NodeMod.PackageManager == "yarn" {
			installCmd = "npm install --cache " + npmCacheDir + " -g yarn; yarn config set yarn-offline-mirror " + yarnCacheDir + "; yarn install"
		} else if g.NodeMod.PackageManager == "npm" {
			installCmd = "npm install --cache " + npmCacheDir
		} else {
			panic("unsupported package manager: " + g.NodeMod.PackageManager)
		}

		in = worker.Run(
			ShArgs(installCmd),
			llb.Dir(joinedWorkDir),
			srcMount,
			WithConstraints(opts...),
		).AddMount(baseDir, in)

		return in
	}
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

// NodeModDeps returns an [llb.State] containing all the node module dependencies for the spec
// for any sources that have a node module generator specified.
// If there are no sources with a node module generator, this will return a nil state.
func (s *Spec) NodeModDeps(sOpt SourceOpts, worker llb.State, opts ...llb.ConstraintsOpt) (*llb.State, error) {
	sources := s.nodeModSources()
	if len(sources) == 0 {
		return nil, nil
	}

	deps := llb.Scratch()

	// Get the patched sources for the node modules
	patched, err := s.getPatchedSources(sOpt, worker, func(name string) bool {
		_, ok := sources[name]
		return ok
	}, opts...)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get patched sources")
	}

	sorted := SortMapKeys(patched)
	opts = append(opts, ProgressGroup("Fetch node module dependencies for source: all before: "))
	for _, key := range sorted {
		src := s.Sources[key]

		deps = deps.With(func(in llb.State) llb.State {
			for _, gen := range src.Generate {
				if gen.NodeMod == nil {
					continue
				}
				in = in.With(withNodeMod(gen, patched[key], worker, opts...))
			}
			return in
		})
	}

	return &deps, nil
}
