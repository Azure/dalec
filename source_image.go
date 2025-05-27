package dalec

import (
	"context"
	stderrors "errors"
	"fmt"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
	"github.com/pkg/errors"
)

type SourceDockerImage struct {
	Ref string   `yaml:"ref" json:"ref"`
	Cmd *Command `yaml:"cmd,omitempty" json:"cmd,omitempty"`
}

// Command is used to execute a command to generate a source from a docker image.
type Command struct {
	// Dir is the working directory to run the command in.
	Dir string `yaml:"dir,omitempty" json:"dir,omitempty"`

	// Mounts is the list of sources to mount into the build steps.
	Mounts []SourceMount `yaml:"mounts,omitempty" json:"mounts,omitempty"`

	// Env is the list of environment variables to set for all commands in this step group.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`

	// Steps is the list of commands to run to generate the source.
	// Steps are run sequentially and results of each step should be cached.
	Steps []*BuildStep `yaml:"steps" json:"steps" jsonschema:"required"`
}

func (cmd *Command) validate() error {
	if cmd == nil {
		return nil
	}

	var errs []error
	if len(cmd.Steps) == 0 {
		errs = append(errs, errors.New("command must have at least one step"))
	}

	for i, step := range cmd.Steps {
		if err := step.validate(); err != nil {
			errs = append(errs, fmt.Errorf("step %d: %w", i, err))
		}
	}

	if len(cmd.Mounts) > 0 {
		dests := make(map[string]struct{}, len(cmd.Mounts))
		for i, mount := range cmd.Mounts {
			if _, ok := dests[mount.Dest]; ok {
				errs = append(errs, fmt.Errorf("cmd mount %d: dest %q: duplicate mount destination", i, mount.Dest))
			}
			dests[mount.Dest] = struct{}{}
			if err := mount.validate(); err != nil {
				errs = append(errs, fmt.Errorf("cmd mount %d: dest %q: %w", i, mount.Dest, err))
			}
		}
	}

	if len(errs) > 0 {
		return stderrors.Join(errs...)
	}
	return nil
}

// BuildStep is used to execute a command to build the artifact(s).
type BuildStep struct {
	// Command is the command to run to build the artifact(s).
	// This will always be wrapped as /bin/sh -c "<command>", or whatever the equivalent is for the target distro.
	Command string `yaml:"command" json:"command" jsonschema:"required"`
	// Env is the list of environment variables to set for the command.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`

	// Mounts is the list of sources to mount into the build step.
	Mounts []SourceMount `yaml:"mounts,omitempty" json:"mounts,omitempty"`
}

func (step *BuildStep) validate() error {
	var errs []error

	if step.Command == "" {
		errs = append(errs, fmt.Errorf("step must have a command"))
	}

	if len(errs) > 0 {
		return stderrors.Join(errs...)
	}

	return nil
}

// SourceMount wraps a [Source] with a target mount point.
type SourceMount struct {
	// Dest is the destination directory to mount to
	Dest string `yaml:"dest" json:"dest" jsonschema:"required"`
	// Spec specifies the source to mount
	Spec Source `yaml:"spec" json:"spec" jsonschema:"required"`
}

func (s SourceMount) SetRunOption(ei *llb.ExecInfo) {
	s.Spec.ToMount(s.Dest, WithConstraint(&ei.Constraints)).SetRunOption(ei)
}

func (m *SourceMount) validate() error {
	var errs []error
	if m.Dest == "/" {
		errs = append(errs, errors.Wrap(errInvalidMountConfig, "mount destination must not be \"/\""))
	}

	if err := m.Spec.validate(); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return stderrors.Join(errs...)
	}
	return nil
}

func (m *SourceMount) validateInRoot(root string) error {
	if err := m.validate(); err != nil {
		return err
	}
	if root != "/" && pathHasPrefix(m.Dest, root) {
		// We cannot support this as the base mount for subPath will shadow the mount being done here.
		return errors.Wrapf(errInvalidMountConfig, "mount destination (%s) must not be a descendent of the target source path (%s)", m.Dest, root)
	}
	return nil
}

func (m *SourceMount) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(string) bool) error {
	if err := m.Spec.processBuildArgs(lex, args, allowArg); err != nil {
		return errors.Wrapf(err, "mount dest: %s", m.Dest)
	}
	return nil
}

func (m *SourceMount) fillDefaults() {
	src := &m.Spec
	src.fillDefaults()
	m.Spec = *src
}

func (src *SourceDockerImage) AsState(name string, path string, sOpt SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, error) {
	st := llb.Image(src.Ref, llb.WithMetaResolver(sOpt.Resolver), withConstraints(opts))
	if src.Cmd == nil {
		return st, nil
	}

	st, err := generateSourceFromImage(st, src.Cmd, sOpt, path, opts...)
	if err != nil {
		return llb.Scratch(), err
	}

	return st, nil
}

// must not be called with a nil cmd pointer
// subPath must be a valid non-empty path
func generateSourceFromImage(st llb.State, cmd *Command, sOpts SourceOpts, subPath string, opts ...llb.ConstraintsOpt) (llb.State, error) {
	if len(cmd.Steps) == 0 {
		return llb.Scratch(), fmt.Errorf("no steps defined for image source")
	}

	if subPath == "" {
		// TODO: We should log a warning here since extracting an entire image while also running a command is
		// probably not what the user really wanted to do here.
		// The buildkit client provides functionality to do this we just need to wire it in.
		subPath = "/"
	}

	for k, v := range cmd.Env {
		st = st.AddEnv(k, v)
	}
	if cmd.Dir != "" {
		st = st.Dir(cmd.Dir)
	}

	baseRunOpts := []llb.RunOption{}

	for _, src := range cmd.Mounts {
		if err := src.validateInRoot(subPath); err != nil {
			return llb.Scratch(), err
		}

		srcSt, err := src.Spec.AsMount(internalMountSourceName, sOpts, opts...)
		if err != nil {
			return llb.Scratch(), err
		}
		var mountOpt []llb.MountOption

		// This handles the case where we are mounting a source with a target extract path and
		// no includes and excludes. In this case, we can extract the path here as a source mount
		// if the source does not handle its own path extraction. This saves an extra llb.Copy operation
		if src.Spec.Path != "" && len(src.Spec.Includes) == 0 && len(src.Spec.Excludes) == 0 &&
			!src.Spec.handlesOwnPath() {
			mountOpt = append(mountOpt, llb.SourcePath(src.Spec.Path))
		}

		if !src.Spec.IsDir() {
			mountOpt = append(mountOpt, llb.SourcePath(internalMountSourceName))
		}
		baseRunOpts = append(baseRunOpts, llb.AddMount(src.Dest, srcSt, mountOpt...))
	}

	out := llb.Scratch()
	for i, step := range cmd.Steps {
		rOpts := []llb.RunOption{llb.Args([]string{"/bin/sh", "-c", step.Command})}

		rOpts = append(rOpts, baseRunOpts...)

		for k, v := range step.Env {
			rOpts = append(rOpts, llb.AddEnv(k, v))
		}

		for _, m := range step.Mounts {
			if err := m.validateInRoot(subPath); err != nil {
				return llb.Scratch(), err
			}

			srcSt, err := m.Spec.AsMount(internalMountSourceName, sOpts, opts...)
			if err != nil {
				return llb.Scratch(), err
			}
			var mountOpt []llb.MountOption
			// This handles the case where we are mounting a source with a target extract path and
			// no includes and excludes. In this case, we can extract the path here as a source mount
			// if the source does not handle its own path extraction. This saves an extra llb.Copy operation
			if m.Spec.Path != "" && len(m.Spec.Includes) == 0 && len(m.Spec.Excludes) == 0 &&
				!m.Spec.handlesOwnPath() {
				mountOpt = append(mountOpt, llb.SourcePath(m.Spec.Path))
			}

			if !SourceIsDir(m.Spec) {
				mountOpt = append(mountOpt, llb.SourcePath(internalMountSourceName))
			}
			rOpts = append(rOpts, llb.AddMount(m.Dest, srcSt, mountOpt...))
		}

		rOpts = append(rOpts, withConstraints(opts))
		cmdSt := st.Run(rOpts...)

		// on first iteration with a root subpath
		// do not use AddMount, as this will overwrite / with a
		// scratch fs
		if i == 0 && subPath == "/" {
			out = cmdSt.Root()
		} else {
			out = cmdSt.AddMount(subPath, out)
		}

		// Update the base state so that changes to the rootfs propagate between
		// steps.
		st = cmdSt.Root()
	}

	return out, nil
}

var errNoImageSourcePath = stderrors.New("docker image source path cannot be empty")

func (src *SourceDockerImage) validate(opts fetchOptions) error {
	var errs []error
	if src.Ref == "" {
		errs = append(errs, errors.New("docker image source must have a ref"))
	}

	if src.Cmd != nil {
		if err := src.Cmd.validate(); err != nil {
			errs = append(errs, err)
		}
	}

	// If someone *really* wants to extract the entire rootfs, they need to say so explicitly.
	// We won't fill this in for them, particularly because this is almost certainly not the user's intent.
	if opts.Path == "" {
		errs = append(errs, errNoImageSourcePath)
	}

	if len(errs) > 0 {
		return stderrors.Join(errs...)
	}
	return nil
}

func (src *SourceDockerImage) IsDir() bool {
	return true
}

func (src *SourceDockerImage) toState(opts fetchOptions) llb.State {
	st := llb.Image(src.Ref, llb.WithMetaResolver(opts.SourceOpt.Resolver), WithConstraints(opts.Constraints...))
	if src.Cmd == nil {
		return st
	}

	return st.With(src.Cmd.toState(opts))
}

func (src *SourceDockerImage) toMount(to string, opts fetchOptions, mountOpts ...llb.MountOption) llb.RunOption {
	st := src.toState(opts)
	return llb.AddMount(to, st, mountOpts...)
}

func (cmd *Command) toState(opts fetchOptions) llb.StateOption {
	return func(img llb.State) llb.State {
		// We've already copied out just the path we want, so use mount filters for just the include/exclude filters.
		return img.With(cmd.baseState(opts)).With(mountFilters(opts))
	}
}

func (cmd *Command) baseState(opts fetchOptions) llb.StateOption {
	return func(img llb.State) llb.State {
		subPath := opts.Path
		if subPath == "" {
			// TODO: We should log a warning here since extracting an entire image while also running a command is
			// probably not what the user really wanted to do here.
			// The buildkit client provides functionality to do this we just need to wire it in.
			subPath = "/"
		}

		for k, v := range cmd.Env {
			img = img.AddEnv(k, v)
		}
		if cmd.Dir != "" {
			img = img.Dir(cmd.Dir)
		}

		baseRunOpts := make([]llb.RunOption, 0, len(cmd.Mounts))
		for _, mnt := range cmd.Mounts {
			if err := mnt.validateInRoot(subPath); err != nil {
				return img.Async(func(ctx context.Context, in llb.State, c *llb.Constraints) (llb.State, error) {
					return in, err
				})
			}
			baseRunOpts = append(baseRunOpts, mnt)
		}

		out := llb.Scratch()
		for i, step := range cmd.Steps {
			rOpts := []llb.RunOption{llb.Args([]string{"/bin/sh", "-c", step.Command})}

			rOpts = append(rOpts, baseRunOpts...)

			for k, v := range step.Env {
				rOpts = append(rOpts, llb.AddEnv(k, v))
			}

			rOpts = append(rOpts, WithConstraints(opts.Constraints...))
			cmdSt := img.Run(rOpts...)

			// on first iteration with a root subpath
			// do not use AddMount, as this will overwrite / with a
			// scratch fs
			if i == 0 && subPath == "/" {
				out = cmdSt.Root()
			} else {
				out = cmdSt.AddMount(subPath, out)
			}

			// Update the base state so that changes to the rootfs propagate between
			// steps.
			img = cmdSt.Root()
		}

		return out
	}
}

func (s *SourceDockerImage) fillDefaults() {
	if s == nil {
		return
	}
	if s.Cmd != nil {
		s.Cmd.fillDefaults()
	}
}

func (s *Command) fillDefaults() {
	if s == nil {
		return
	}

	for i, mnt := range s.Mounts {
		m := &mnt
		m.fillDefaults()
		s.Mounts[i] = *m
	}
}
