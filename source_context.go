package dalec

import (
	"context"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
	"github.com/pkg/errors"
)

// SourceContext is used to generate a source from a build context. The path to
// the build context is provided to the `Path` field of the owning `Source`.
type SourceContext struct {
	// Name is the name of the build context. By default, it is the magic name
	// `context`, recognized by Docker as the default context.
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
}

func (src *SourceContext) AsState(path string, includes []string, excludes []string, sOpt SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, error) {
	if !isRoot(path) {
		excludes = append(excludeAllButPath(path), excludes...)
	}

	st, err := sOpt.GetContext(src.Name, localIncludeExcludeMerge(includes, excludes), withFollowPath(path), withConstraints(opts))
	if err != nil {
		return llb.Scratch(), err
	}

	if st == nil {
		return llb.Scratch(), errors.Errorf("context %q not found", src.Name)
	}

	return *st, nil
}

func (src *SourceContext) validate(opts fetchOptions) error {
	return nil
}

func (src *SourceContext) IsDir() bool {
	return true
}

func (src *SourceContext) baseState(opts fetchOptions) llb.State {
	return llb.Scratch().Async(func(ctx context.Context, _ llb.State, _ *llb.Constraints) (llb.State, error) {
		st, err := opts.SourceOpt.GetContext(src.Name, opts)
		if err != nil {
			return llb.Scratch(), err
		}

		if st == nil {
			return llb.Scratch(), errors.Errorf("context %q not found", src.Name)
		}
		return *st, nil
	})
}

func (src *SourceContext) toState(opts fetchOptions) llb.State {
	st := src.baseState(opts).With(sourceFilters(opts))
	return st
}

func (src *SourceContext) toMount(to string, opts fetchOptions, mountOpts ...llb.MountOption) llb.RunOption {
	st := src.baseState(opts).With(mountFilters(opts))
	mountOpts = append(mountOpts, llb.SourcePath(opts.Path))
	return llb.AddMount(to, st, mountOpts...)
}

func (src *SourceContext) fillDefaults() {
	if src.Name == "" {
		src.Name = "context"
	}
}

func (src *SourceContext) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(key string) bool) error {
	updated, err := expandArgs(lex, src.Name, args, allowArg)
	if err != nil {
		return errors.Wrapf(err, "could not expand context name %q", src.Name)
	}
	src.Name = updated
	return nil
}
