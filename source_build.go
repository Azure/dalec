package dalec

import (
	"context"
	goerrors "errors"
	"fmt"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerui"
	"github.com/pkg/errors"
)

// SourceBuild is used to generate source from a DockerFile build.
type SourceBuild struct {
	// A source specification to use as the context for the Dockerfile build
	Source Source `yaml:"source,omitempty" json:"source,omitempty"`

	// DockerfilePath is the path to the build file in the build context
	// If not set the default is assumed by buildkit to be `Dockerfile` at the root of the context.
	DockerfilePath string `yaml:"dockerfile_path,omitempty" json:"dockerfile_path,omitempty"`

	// Target specifies the build target to use.
	// If unset, the default target is determined by the frontend implementation
	// (e.g. the dockerfile frontend uses the last build stage as the default).
	Target string `yaml:"target,omitempty" json:"target,omitempty"`
	// Args are the build args to pass to the build.
	Args map[string]string `yaml:"args,omitempty" json:"args,omitempty"`
}

func (s *SourceBuild) validate(fetchOptions) error {
	var errs []error
	if s.Source.Build != nil {
		errs = append(errs, fmt.Errorf("build sources cannot be recursive"))
	}

	if err := s.Source.validate(); err != nil {
		errs = append(errs, fmt.Errorf("build source: %w", err))
	}

	if len(errs) == 0 {
		return nil
	}
	return goerrors.Join(errs...)
}

func (src *SourceBuild) AsState(name string, sOpt SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, error) {
	if src.Source.Inline != nil && src.Source.Inline.File != nil {
		name = src.DockerfilePath
		if name == "" {
			name = dockerui.DefaultDockerfileName
		}
	}

	st, err := src.Source.AsState(name, sOpt, opts...)
	if err != nil {
		if !errors.Is(err, errNoSourceVariant) {
			return llb.Scratch(), err
		}
		st = llb.Scratch()
	}

	st, err = sOpt.Forward(st, src, opts...)
	if err != nil {
		return llb.Scratch(), err
	}

	return st, nil
}

func (src *SourceBuild) baseState(opts fetchOptions) llb.State {
	name := opts.Rename
	if src.Source.Inline != nil && src.Source.Inline.File != nil {
		name = src.DockerfilePath
		if name == "" {
			name = dockerui.DefaultDockerfileName
		}
	}

	st := src.Source.ToState(name, opts.Constraints...)

	return st.Async(func(ctx context.Context, in llb.State, c *llb.Constraints) (llb.State, error) {
		// prepend the constraints passed into the async call to the ones from the source
		cOpts := []llb.ConstraintsOpt{WithConstraint(c)}
		cOpts = append(cOpts, opts.Constraints...)
		return opts.SourceOpt.Forward(in, src, cOpts...)
	})
}

func (src *SourceBuild) IsDir() bool {
	return true
}

func (src *SourceBuild) toState(opts fetchOptions) llb.State {
	return src.baseState(opts).With(sourceFilters(opts))
}

func (src *SourceBuild) toMount(to string, opts fetchOptions, mountOpts ...llb.MountOption) llb.RunOption {
	st := src.baseState(opts).With(mountFilters(opts))
	return llb.AddMount(to, st, mountOpts...)
}

func (src *SourceBuild) fillDefaults() {
	bsrc := &src.Source
	bsrc.fillDefaults()
	src.Source = *bsrc
}
