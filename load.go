package dalec

import (
	goerrors "errors"
	"fmt"
	"os"
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
	case "SOURCE_DATE_EPOCH":
		return true
	}

	return platformArg(key)
}

func platformArg(key string) bool {
	switch key {
	case "TARGETOS", "TARGETARCH", "TARGETPLATFORM", "TARGETVARIANT",
		"BUILDOS", "BUILDARCH", "BUILDPLATFORM", "BUILDVARIANT":
		return true
	default:
		return false
	}
}

const DefaultPatchStrip int = 1

func expandArgs(lex *shell.Lex, s string, args map[string]string) (string, *ErrorList) {
	result, err := lex.ProcessWordWithMatches(s, args)
	if err != nil {
		return "", AsList(err)
	}

	var errL ErrorList
	for m := range result.Unmatched {
		if !knownArg(m) {
			errL.Append(fmt.Errorf(`build arg "%s" not declared`, m))
		}
	}

	return result.Result, &errL
}

func (s *Source) substituteBuildArgs(args map[string]string) *ErrorList {
	lex := shell.NewLex('\\')
	// force the shell lexer to skip unresolved env vars so they aren't
	// replaced with ""
	lex.SkipUnsetEnv = true

	var errL = &ErrorList{}

	switch {
	case s.DockerImage != nil:
		updated, errs := expandArgs(lex, s.DockerImage.Ref, args)
		s.DockerImage.Ref = updated

		errL = CombineErrorList(errL, errs)

		if s.DockerImage.Cmd != nil {
			for _, mnt := range s.DockerImage.Cmd.Mounts {
				errs := mnt.Spec.substituteBuildArgs(args)
				errL = CombineErrorList(errL, errs)
			}
		}
	case s.Git != nil:
		updated, errs := expandArgs(lex, s.Git.URL, args)
		s.Git.URL = updated
		errL = CombineErrorList(errL, errs)

		updated, errs = expandArgs(lex, s.Git.Commit, args)
		s.Git.Commit = updated
		errL = CombineErrorList(errL, errs)

	case s.HTTP != nil:
		updated, errs := expandArgs(lex, s.HTTP.URL, args)
		errL = CombineErrorList(errL, errs)

		s.HTTP.URL = updated
	case s.Context != nil:
		updated, errs := expandArgs(lex, s.Context.Name, args)
		s.Context.Name = updated
		errL = CombineErrorList(errL, errs)
	case s.Build != nil:
		errs := s.Build.Source.substituteBuildArgs(args)
		errL = CombineErrorList(errL, errs)

		updated, errs := expandArgs(lex, s.Build.DockerfilePath, args)
		errL = CombineErrorList(errL, errs)
		s.Build.DockerfilePath = updated

		updated, errs = expandArgs(lex, s.Build.Target, args)
		errL = CombineErrorList(errL, errs)
		s.Build.Target = updated
	}

	return errL
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
		if s.Context.Name == "" {
			s.Context.Name = dockerui.DefaultLocalNameContext
		}
	case s.Build != nil:
		fillDefaults(&s.Build.Source)
	case s.Inline != nil:
	}
}

func (s *Source) validate(failContext ...string) (retErr error) {
	count := 0

	defer func() {
		if retErr != nil && failContext != nil {
			retErr = errors.Wrap(retErr, strings.Join(failContext, " "))
		}
	}()

	for _, g := range s.Generate {
		if err := g.Validate(); err != nil {
			retErr = goerrors.Join(retErr, err)
		}
	}

	if s.DockerImage != nil {
		if s.DockerImage.Ref == "" {
			retErr = goerrors.Join(retErr, fmt.Errorf("docker image source variant must have a ref"))
		}

		if s.DockerImage.Cmd != nil {
			// If someone *really* wants to extract the entire rootfs, they need to say so explicitly.
			// We won't fill this in for them, particularly because this is almost certainly not the user's intent.
			if s.Path == "" {
				retErr = goerrors.Join(retErr, errors.Errorf("source path cannot be empty"))
			}

			for _, mnt := range s.DockerImage.Cmd.Mounts {
				if err := mnt.validate(s.Path); err != nil {
					retErr = goerrors.Join(retErr, err)
				}
				if err := mnt.Spec.validate("docker image source with ref", "'"+s.DockerImage.Ref+"'"); err != nil {
					retErr = goerrors.Join(retErr, err)
				}
			}
		}

		count++
	}

	if s.Git != nil {
		count++
	}
	if s.HTTP != nil {
		if err := s.HTTP.validate(); err != nil {
			retErr = goerrors.Join(retErr, err)
		}
		count++
	}
	if s.Context != nil {
		count++
	}
	if s.Build != nil {
		c := s.Build.DockerfilePath
		if err := s.Build.validate("build source with dockerfile", "`"+c+"`"); err != nil {
			retErr = goerrors.Join(retErr, err)
		}

		count++
	}

	if s.Inline != nil {
		if err := s.Inline.validate(s.Path); err != nil {
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

var errUnknownArg = errors.New("unknown arg")

func (s *Spec) SubstituteArgs(env map[string]string) error {
	lex := shell.NewLex('\\')
	// force the shell lexer to skip unresolved env vars so they aren't
	// replaced with ""
	lex.SkipUnsetEnv = true

	var errL *ErrorList = &ErrorList{}

	args := make(map[string]string)
	for k, v := range s.Args {
		args[k] = v
	}
	for k, v := range env {
		if _, ok := args[k]; !ok {
			if !knownArg(k) {
				errL.Append(fmt.Errorf("%w: %q", errUnknownArg, k))
			}

			// if the build arg isn't present in args by opt-in, skip
			// and don't automatically inject a value
			continue
		}

		args[k] = v
	}

	for name, src := range s.Sources {
		if errs := src.substituteBuildArgs(args); !errs.Empty() {
			errL.Append(fmt.Errorf("error performing shell expansion on source %q: %w", name, errs.Join()))
		}
		if src.DockerImage != nil {
			if err := src.DockerImage.Cmd.processBuildArgs(lex, args, name); err != nil {
				errL.Append(fmt.Errorf("error performing shell expansion on source %q: %w", name, err))
			}
		}
		s.Sources[name] = src
	}

	updated, errs := expandArgs(lex, s.Version, args)
	if !errs.Empty() {
		errL.Append(fmt.Errorf("error performing shell expansion on version: %w", errs.Join()))
	}
	s.Version = updated

	updated, errs = expandArgs(lex, s.Revision, args)
	if !errs.Empty() {
		errL.Append(fmt.Errorf("error performing shell expansion on revision: %w", errs.Join()))
	}
	s.Revision = updated

	for k, v := range s.Build.Env {
		updated, errs := expandArgs(lex, v, args)
		if !errs.Empty() {
			errL.Append(fmt.Errorf("error performing shell expansion on env var %q: %w", k, errs.Join()))
		}
		s.Build.Env[k] = updated
	}

	for i, step := range s.Build.Steps {
		bs := &step
		if err := bs.processBuildArgs(lex, args, i); err != nil {
			errL.Append(fmt.Errorf("error performing shell expansion on build step %d: %w", i, err))
		}
		s.Build.Steps[i] = *bs
	}

	for _, t := range s.Tests {
		if err := t.processBuildArgs(lex, args, t.Name); err != nil {
			errL.Append(fmt.Errorf("error performing shell expansion on test %q: %w", t.Name, err))
		}
	}

	for name, t := range s.Targets {
		if err := t.processBuildArgs(name, lex, args); err != nil {
			errL.Append(fmt.Errorf("error processing build args for target %q: %w", name, err))
		}
	}

	if s.PackageConfig != nil {
		if err := s.PackageConfig.processBuildArgs(lex, args); err != nil {
			errL.Append(fmt.Errorf("could not process build args for base spec package config: %w", err))
		}
	}

	return errL.Join()
}

// LoadSpec loads a spec from the given data.
func LoadSpec(dt []byte) (*Spec, error) {
	var spec Spec

	dt, err := stripXFields(dt)
	if err != nil {
		return nil, fmt.Errorf("error stripping x-fields: %w", err)
	}

	if err := yaml.UnmarshalWithOptions(dt, &spec, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("error unmarshalling spec: %w", err)
	}

	if err := spec.Validate(); err != nil {
		return nil, err
	}
	spec.FillDefaults()

	return &spec, nil
}

func stripXFields(dt []byte) ([]byte, error) {
	var obj map[string]interface{}
	if err := yaml.Unmarshal(dt, &obj); err != nil {
		return nil, fmt.Errorf("error unmarshalling spec: %w", err)
	}

	for k := range obj {
		if strings.HasPrefix(k, "x-") || strings.HasPrefix(k, "X-") {
			delete(obj, k)
		}
	}

	return yaml.Marshal(obj)
}

func (s *BuildStep) processBuildArgs(lex *shell.Lex, args map[string]string, i int) error {
	var errL = &ErrorList{}
	for k, v := range s.Env {
		updated, errs := expandArgs(lex, v, args)
		if !errs.Empty() {
			errL.Append(fmt.Errorf("error performing shell expansion on env var %q for step %d: %w", k, i, errs.Join()))
		}
		s.Env[k] = updated
	}
	return errL.Join()
}

func (c *Command) processBuildArgs(lex *shell.Lex, args map[string]string, name string) error {
	if c == nil {
		return nil
	}

	var errL = &ErrorList{}
	for _, s := range c.Mounts {
		if errs := s.Spec.substituteBuildArgs(args); !errs.Empty() {
			errL.Append(fmt.Errorf("error performing shell expansion on source ref %q: %w", name, errs.Join()))
		}
	}
	for k, v := range c.Env {
		updated, errs := expandArgs(lex, v, args)
		if !errs.Empty() {
			errL.Append(fmt.Errorf("error performing shell expansion on env var %q for source %q: %w", k, name, errs.Join()))
		}
		c.Env[k] = updated
	}
	for i, step := range c.Steps {
		for k, v := range step.Env {
			updated, errs := expandArgs(lex, v, args)
			if !errs.Empty() {
				errL.Append(fmt.Errorf("error performing shell expansion on env var %q for source %q: %w", k, name, errs.Join()))
			}
			step.Env[k] = updated
			c.Steps[i] = step
		}
	}

	return errL.Join()
}

func (s *Spec) FillDefaults() {
	for name, src := range s.Sources {
		fillDefaults(&src)
		s.Sources[name] = src
	}

	for k, patches := range s.Patches {
		for i, ps := range patches {
			if ps.Strip != nil {
				continue
			}
			strip := DefaultPatchStrip
			s.Patches[k][i].Strip = &strip
		}
	}
}

func (s Spec) Validate() error {
	for name, src := range s.Sources {
		if strings.ContainsRune(name, os.PathSeparator) {
			return &InvalidSourceError{Name: name, Err: sourceNamePathSeparatorError}
		}
		if err := src.validate(); err != nil {
			return &InvalidSourceError{Name: name, Err: fmt.Errorf("error validating source ref %q: %w", name, err)}
		}

		if src.DockerImage != nil && src.DockerImage.Cmd != nil {
			for p, cfg := range src.DockerImage.Cmd.CacheDirs {
				if _, err := sharingMode(cfg.Mode); err != nil {
					return &InvalidSourceError{Name: name, Err: errors.Wrapf(err, "invalid sharing mode for source %q with cache mount at path %q", name, p)}
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
		updated, errs := expandArgs(lex, contains, args)
		if !errs.Empty() {
			return errors.Wrap(errs.Join(), "error performing shell expansion on contains")
		}
		c.Contains[i] = updated
	}

	updated, errs := expandArgs(lex, c.EndsWith, args)
	if !errs.Empty() {
		return errors.Wrap(errs.Join(), "error performing shell expansion on endsWith")
	}
	c.EndsWith = updated

	updated, errs = expandArgs(lex, c.Matches, args)
	if !errs.Empty() {
		return errors.Wrap(errs.Join(), "error performing shell expansion on matches")
	}
	c.Matches = updated

	updated, errs = expandArgs(lex, c.Equals, args)
	if !errs.Empty() {
		return errors.Wrap(errs.Join(), "error performing shell expansion on equals")
	}
	c.Equals = updated

	updated, errs = expandArgs(lex, c.StartsWith, args)
	if !errs.Empty() {
		return errors.Wrap(errs.Join(), "error performing shell expansion on startsWith")
	}
	c.StartsWith = updated
	return nil
}

func (c *TestSpec) processBuildArgs(lex *shell.Lex, args map[string]string, name string) error {
	var errL = &ErrorList{}
	for _, s := range c.Mounts {
		errs := s.Spec.substituteBuildArgs(args)
		if !errs.Empty() {
			errL.Append(fmt.Errorf("error performing shell expansion on source ref %q: %w", name, errs.Join()))
		}
	}

	for k, v := range c.Env {
		updated, errs := expandArgs(lex, v, args)
		if !errs.Empty() {
			errL.Append(fmt.Errorf("error performing shell expansion on env var %q for source %q: %w", k, name, errs.Join()))
		}
		c.Env[k] = updated
	}

	for i, step := range c.Steps {
		for k, v := range step.Env {
			updated, errs := expandArgs(lex, v, args)
			if !errs.Empty() {
				errL.Append(fmt.Errorf("error performing shell expansion on env var %q for source %q: %w", k, name, errs.Join()))
			}
			step.Env[k] = updated
			c.Steps[i] = step
		}
	}

	for i, step := range c.Steps {
		stdout := step.Stdout
		if err := stdout.processBuildArgs(lex, args); err != nil {
			errL.Append(err)
		}
		step.Stdout = stdout

		stderr := step.Stderr
		if err := stderr.processBuildArgs(lex, args); err != nil {
			errL.Append(err)
		}

		step.Stderr = stderr
		c.Steps[i] = step
	}

	for name, f := range c.Files {
		if err := f.processBuildArgs(lex, args); err != nil {
			errL.Append(errors.Wrap(err, name))
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

func (g *SourceGenerator) Validate() error {
	if g.Gomod == nil {
		// Gomod is the only valid generator type
		// An empty generator is invalid
		return fmt.Errorf("no generator type specified")
	}
	return nil
}

func (s *PackageSigner) processBuildArgs(lex *shell.Lex, args map[string]string) error {
	for k, v := range s.Args {
		updated, err := lex.ProcessWordWithMap(v, args)
		if err != nil {
			return fmt.Errorf("error performing shell expansion on env var %q: %w", k, err)
		}
		s.Args[k] = updated
	}
	return nil
}

func (t *Target) processBuildArgs(name string, lex *shell.Lex, args map[string]string) error {
	for _, tt := range t.Tests {
		if err := tt.processBuildArgs(lex, args, path.Join(name, tt.Name)); err != nil {
			return err
		}
	}

	if t.PackageConfig != nil {
		if err := t.PackageConfig.processBuildArgs(lex, args); err != nil {
			return fmt.Errorf("error processing package config build args: %w", err)
		}
	}

	return nil
}

func (cfg *PackageConfig) processBuildArgs(lex *shell.Lex, args map[string]string) error {
	if cfg.Signer != nil {
		if err := cfg.Signer.processBuildArgs(lex, args); err != nil {
			return fmt.Errorf("could not process build args for signer config: %w", err)
		}
	}

	return nil
}
