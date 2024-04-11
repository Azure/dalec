package dalec

import (
	goerrors "errors"
	"fmt"
	"strings"

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

func (s *SourceBuild) validate(failContext ...string) (retErr error) {
	defer func() {
		if retErr != nil && failContext != nil {
			retErr = errors.Wrap(retErr, strings.Join(failContext, " "))
		}
	}()

	if s.Source.Build != nil {
		return goerrors.Join(retErr, fmt.Errorf("build sources cannot be recursive"))
	}

	if err := s.Source.validate("build subsource"); err != nil {
		retErr = goerrors.Join(retErr, err)
	}

	return
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

	st, err = sOpt.Forward(st, src)
	if err != nil {
		return llb.Scratch(), err
	}

	return st, nil
}
