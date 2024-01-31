package dalec

import (
	goerrors "errors"
	"fmt"
	"path"
	"strings"

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

func (s *Source) substituteBuildArgs(args map[string]string) error {
	lex := shell.NewLex('\\')

	switch {
	case s.DockerImage != nil:
		updated, err := lex.ProcessWordWithMap(s.DockerImage.Ref, args)
		if err != nil {
			return err
		}
		s.DockerImage.Ref = updated

		if s.DockerImage.Cmd != nil {
			for _, mnt := range s.DockerImage.Cmd.Mounts {
				if err := mnt.Spec.substituteBuildArgs(args); err != nil {
					return err
				}
			}
		}
	case s.Git != nil:
		updated, err := lex.ProcessWordWithMap(s.Git.URL, args)
		if err != nil {
			return err
		}
		s.Git.URL = updated

		updated, err = lex.ProcessWordWithMap(s.Git.Commit, args)
		if err != nil {
			return err
		}
		s.Git.Commit = updated
	case s.HTTP != nil:
		updated, err := lex.ProcessWordWithMap(s.HTTP.URL, args)
		if err != nil {
			return err
		}
		s.HTTP.URL = updated
	case s.Context != nil:
		updated, err := lex.ProcessWordWithMap(s.Context.Name, args)
		if err != nil {
			return err
		}
		s.Context.Name = updated
	case s.Build != nil:
		if err := s.Build.Source.substituteBuildArgs(args); err != nil {
			return err
		}

		updated, err := lex.ProcessWordWithMap(s.Build.DockerFile, args)
		if err != nil {
			return err
		}
		s.Build.DockerFile = updated

		updated, err = lex.ProcessWordWithMap(s.Build.Target, args)
		if err != nil {
			return err
		}
		s.Build.Target = updated
	}

	return nil
}

func fillDefaults(s *Source) {
	switch {
	case s.DockerImage != nil:
		if s.DockerImage.Cmd != nil {
			for _, mnt := range s.DockerImage.Cmd.Mounts {
				fillDefaults(&mnt.Spec)
			}
		}
	case s.Git != nil:
	case s.HTTP != nil:
	case s.Context != nil:
		origContextName := s.Context.Name
		if s.Context.Name == "" {
			s.Context.Name = dockerui.DefaultLocalNameContext
		}

		ctxPath := origContextName
		if ctxPath == "" || ctxPath == dockerui.DefaultLocalNameContext {
			ctxPath = "."
		}
		s.Path = ctxPath
	case s.Build != nil:
		fillDefaults(&s.Build.Source)
	}
}

func (s *Source) validate(failContext ...string) (retErr error) {
	count := 0

	defer func() {
		if retErr != nil && failContext != nil {
			retErr = errors.Wrap(retErr, strings.Join(failContext, " "))
		}
	}()

	if s.DockerImage != nil {
		if s.DockerImage.Ref == "" {
			retErr = goerrors.Join(retErr, fmt.Errorf("docker image source variant must have a ref"))
		}

		if s.DockerImage.Cmd != nil {
			for _, mnt := range s.DockerImage.Cmd.Mounts {
				if err := mnt.Spec.validate("docker image source with ref", "'"+s.DockerImage.Ref+"'"); err != nil {
					retErr = goerrors.Join(retErr, err)
				}
			}
		}

		count++
	}
	if retErr != nil {
		return retErr
	}

	if s.Git != nil {
		count++
	}
	if s.HTTP != nil {
		count++
	}
	if s.Context != nil {
		count++
	}
	if s.Build != nil {
		c := s.Build.DockerFile
		if s.Build.Inline != "" {
			c = s.Build.Inline
		}

		if err := s.Build.validate("build source with dockerfile", "`"+c+"`"); err != nil {
			retErr = goerrors.Join(retErr, err)
		}

		count++
	}

	switch count {
	case 0:
		retErr = goerrors.Join(retErr, fmt.Errorf("no non-nil source variant"))
	case 1:
		return retErr
	default:
		retErr = goerrors.Join(retErr, fmt.Errorf("more than one source variant defined"))
	}

	return retErr
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

	if s.DockerFile != "" && s.Inline != "" {
		retErr = goerrors.Join(retErr, fmt.Errorf("build sources may use either `dockerfile` or `inline`, but not both"))
	}

	if s.DockerFile == "" && s.Inline == "" {
		retErr = goerrors.Join(retErr, fmt.Errorf("build must use either `dockerfile` or `inline`"))
	}

	if err := s.Source.validate("build subsource"); err != nil {
		retErr = goerrors.Join(retErr, err)
	}

	return
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

		if err := src.substituteBuildArgs(args); err != nil {
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
		if err := s.Spec.substituteBuildArgs(args); err != nil {
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
