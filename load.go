package dalec

import (
	goerrors "errors"
	"fmt"
	"path"

	"github.com/goccy/go-yaml"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
	"github.com/moby/buildkit/frontend/dockerui"
	"github.com/pkg/errors"
)

func knownArg(key string) bool {
	switch key {
	case "BUILDKIT_SYNTAX":
		return true
	case "DALEC_DISABLE_DIFF_MERGE":
		return true
	default:
		return false
	}
}

const DefaultPatchStrip int = 1

func (s *Source) processArgs(args map[string]string) error {
	lex := shell.NewLex('\\')

	var sub *string
	switch {
	case s.DockerImage != nil:
		sub = &s.DockerImage.Ref
	case s.Git != nil:
		sub = &s.Git.URL
	case s.HTTPS != nil:
		sub = &s.HTTPS.URL
	case s.Context != nil:
		updated, err := lex.ProcessWordWithMap(s.Context.Name, args)
		if err != nil {
			return err
		}
		s.Context.Name = updated

		updated, err = lex.ProcessWordWithMap(s.Context.Path, args)
		if err != nil {
			return err
		}
		s.Context.Path = updated

		return nil
	case s.Build != nil:
		sub = &s.Build.ContextPath
	default:
	}

	if sub == nil {
		return nil
	}

	updated, err := lex.ProcessWordWithMap(*sub, args)
	if err != nil {
		return err
	}

	*sub = updated
	return nil
}

func fillDefaults(s *Source) {
	switch {
	case s.DockerImage != nil:
	case s.Git != nil:
	case s.HTTPS != nil:
	case s.Context != nil:
		if s.Context.Name == "" {
			s.Context.Name = dockerui.DefaultLocalNameContext
		}
		if s.Context.Path == "" {
			s.Context.Path = "."
		}
	case s.Build != nil:
		if s.Build.ContextPath == "" {
			s.Build.ContextPath = "."
		}
	default:
	}
}

func (s *Source) validate() error {
	count := 0
	var errs error

	if s.DockerImage != nil {
		count++
	}
	if s.Git != nil {
		count++
	}
	if s.HTTPS != nil {
		count++
	}
	if s.Context != nil {
		count++
	}
	if s.Build != nil {
		if s.Build.DockerFile != "" && s.Build.Inline != "" {
			errs = goerrors.Join(errs, fmt.Errorf("build sources may use either `dockerfile` or `inline`, but not both"))
		}
		count++
	}

	switch count {
	case 0:
		errs = goerrors.Join(errs, fmt.Errorf("no non-nil source variant"))
	case 1:
		// success condition
	default:
		errs = goerrors.Join(errs, fmt.Errorf("more than one source variant defined"))
	}

	return errs
}

// LoadSpec loads a spec from the given data.
// env is a map of environment variables to use for shell-style expansion in the spec.
func LoadSpec(dt []byte, env map[string]string) (*Spec, error) {
	var spec Spec
	if err := yaml.Unmarshal(dt, &spec); err != nil {
		return nil, fmt.Errorf("error unmarshalling spec: %w", err)
	}

	lex := shell.NewLex('\\')

	args := make(map[string]string)
	for k, v := range spec.Args {
		args[k] = v
	}
	for k, v := range env {
		if _, ok := args[k]; !ok {
			if !knownArg(k) {
				return nil, fmt.Errorf("unknown arg %q", k)
			}
		}
		args[k] = v
	}

	for name, src := range spec.Sources {
		if err := src.validate(); err != nil {
			return nil, fmt.Errorf("error validating source ref %q: %w", name, err)
		}

		fillDefaults(&src)

		if err := src.processArgs(args); err != nil {
			return nil, fmt.Errorf("error performing shell expansion on source %q: %w", name, err)
		}
		if src.DockerImage != nil {
			if err := src.DockerImage.Cmd.processBuildArgs(lex, args, name); err != nil {
				return nil, fmt.Errorf("error performing shell expansion on source %q: %w", name, err)
			}
		}
		spec.Sources[name] = src
	}

	updated, err := lex.ProcessWordWithMap(spec.Version, args)
	if err != nil {
		return nil, fmt.Errorf("error performing shell expansion on version: %w", err)
	}
	spec.Version = updated

	updated, err = lex.ProcessWordWithMap(spec.Revision, args)
	if err != nil {
		return nil, fmt.Errorf("error performing shell expansion on revision: %w", err)
	}
	spec.Revision = updated

	for k, v := range spec.Build.Env {
		updated, err := lex.ProcessWordWithMap(v, args)
		if err != nil {
			return nil, fmt.Errorf("error performing shell expansion on env var %q: %w", k, err)
		}
		spec.Build.Env[k] = updated

	}

	for i, step := range spec.Build.Steps {
		s := &step
		if err := s.processBuildArgs(lex, args, i); err != nil {
			return nil, fmt.Errorf("error performing shell expansion on build step %d: %w", i, err)
		}
		spec.Build.Steps[i] = *s
	}

	for _, t := range spec.Tests {
		if err := t.processBuildArgs(lex, args, t.Name); err != nil {
			return nil, err
		}
	}

	for name, target := range spec.Targets {
		for _, t := range target.Tests {
			if err := t.processBuildArgs(lex, args, path.Join(name, t.Name)); err != nil {
				return nil, err
			}
		}
	}

	for k, patches := range spec.Patches {
		for i, ps := range patches {
			if ps.Strip != nil {
				continue
			}
			strip := DefaultPatchStrip
			spec.Patches[k][i].Strip = &strip
		}
	}

	return &spec, spec.Validate()
}

func (s *BuildStep) processBuildArgs(lex *shell.Lex, args map[string]string, i int) error {
	for k, v := range s.Env {
		updated, err := lex.ProcessWordWithMap(v, args)
		if err != nil {
			return fmt.Errorf("error performing shell expansion on env var %q for step %d: %w", k, i, err)
		}
		s.Env[k] = updated
	}
	return nil
}

func (c *Command) processBuildArgs(lex *shell.Lex, args map[string]string, name string) error {
	if c == nil {
		return nil
	}
	for _, s := range c.Mounts {
		if err := s.Spec.processArgs(args); err != nil {
			return fmt.Errorf("error performing shell expansion on source ref %q: %w", name, err)
		}
	}
	for k, v := range c.Env {
		updated, err := lex.ProcessWordWithMap(v, args)
		if err != nil {
			return fmt.Errorf("error performing shell expansion on env var %q for source %q: %w", k, name, err)
		}
		c.Env[k] = updated
	}
	for i, step := range c.Steps {
		for k, v := range step.Env {
			updated, err := lex.ProcessWordWithMap(v, args)
			if err != nil {
				return fmt.Errorf("error performing shell expansion on env var %q for source %q: %w", k, name, err)
			}
			step.Env[k] = updated
			c.Steps[i] = step
		}
	}

	return nil
}

func (s Spec) Validate() error {
	for name, src := range s.Sources {
		if src.DockerImage != nil && src.DockerImage.Cmd != nil {
			for p, cfg := range src.DockerImage.Cmd.CacheDirs {
				if _, err := sharingMode(cfg.Mode); err != nil {
					return errors.Wrapf(err, "invalid sharing mode for source %q with cache mount at path %q", name, p)
				}
			}
		}
	}

	for _, t := range s.Tests {
		for p, cfg := range t.CacheDirs {
			if _, err := sharingMode(cfg.Mode); err != nil {
				return errors.Wrapf(err, "invalid sharing mode for test %q with cache mount at path %q", t.Name, p)
			}
		}
	}

	return nil
}

func (c *CheckOutput) processBuildArgs(lex *shell.Lex, args map[string]string) error {
	for i, contains := range c.Contains {
		updated, err := lex.ProcessWordWithMap(contains, args)
		if err != nil {
			return errors.Wrap(err, "error performing shell expansion on contains")
		}
		c.Contains[i] = updated
	}

	updated, err := lex.ProcessWordWithMap(c.EndsWith, args)
	if err != nil {
		return errors.Wrap(err, "error performing shell expansion on endsWith")
	}
	c.EndsWith = updated

	updated, err = lex.ProcessWordWithMap(c.Matches, args)
	if err != nil {
		return errors.Wrap(err, "error performing shell expansion on matches")
	}
	c.Matches = updated

	updated, err = lex.ProcessWordWithMap(c.Equals, args)
	if err != nil {
		return errors.Wrap(err, "error performing shell expansion on equals")
	}
	c.Equals = updated

	updated, err = lex.ProcessWordWithMap(c.StartsWith, args)
	if err != nil {
		return errors.Wrap(err, "error performing shell expansion on startsWith")
	}
	c.StartsWith = updated
	return nil
}

func (c *TestSpec) processBuildArgs(lex *shell.Lex, args map[string]string, name string) error {
	if err := c.Command.processBuildArgs(lex, args, name); err != nil {
		return err
	}

	for i, step := range c.Steps {
		stdout := step.Stdout
		if err := stdout.processBuildArgs(lex, args); err != nil {
			return err
		}
		step.Stdout = stdout

		stderr := step.Stderr
		if err := stderr.processBuildArgs(lex, args); err != nil {
			return err
		}
		step.Stderr = stderr

		c.Steps[i] = step
	}

	for name, f := range c.Files {
		if err := f.processBuildArgs(lex, args); err != nil {
			return errors.Wrap(err, name)
		}
		c.Files[name] = f
	}

	return nil
}

func (c *FileCheckOutput) processBuildArgs(lex *shell.Lex, args map[string]string) error {
	check := c.CheckOutput
	if err := check.processBuildArgs(lex, args); err != nil {
		return err
	}
	c.CheckOutput = check
	return nil
}
