package dalec

import (
	"fmt"
	"path"

	"github.com/goccy/go-yaml"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
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
		sub = &s.Context.Name
		if *sub == "" {
			*sub = "context"
		}
	case s.Build != nil:
		switch {
		case s.Build.Context != nil:
			sub = &s.Build.Context.Name
		case s.Build.Local != nil:
			sub = &s.Build.Local.Path
		case s.Build.Source != nil:
			sub = &s.Build.Source.Name
		}
	case s.Local != nil:
		sub = &s.Local.Path
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

func checkDuplicate[T any](c *int, p *T) error {
	if c == nil {
		return fmt.Errorf("validation function (check) called with nil pointer")
	}

	if p != nil {
		(*c)++
	}

	if *c > 1 {
		return fmt.Errorf("more than one source variant defined")
	}

	return nil
}

func (s *Source) validate() error {
	count := 0

	if err := checkDuplicate(&count, s.DockerImage); err != nil {
		return err
	}
	if err := checkDuplicate(&count, s.Git); err != nil {
		return err
	}
	if err := checkDuplicate(&count, s.HTTPS); err != nil {
		return err
	}
	if err := checkDuplicate(&count, s.Context); err != nil {
		return err
	}
	if err := checkDuplicate(&count, s.Build); err != nil {
		return err
	}
	if err := checkDuplicate(&count, s.Local); err != nil {
		return err
	}

	if count == 0 {
		return fmt.Errorf("source has no non-nil variant")
	}

	return nil
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

		if err := src.processArgs(args); err != nil {
			return nil, fmt.Errorf("error performing shell expansion on source ref %q: %w", name, err)
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
