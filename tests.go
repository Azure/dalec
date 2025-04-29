package dalec

import (
	goerrors "errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	"github.com/moby/buildkit/frontend/dockerfile/shell"
	"github.com/pkg/errors"
)

// TestSpec is used to execute tests against a container with the package installed in it.
type TestSpec struct {
	// Name is the name of the test
	// This will be used to output the test results
	Name string `yaml:"name" json:"name" jsonschema:"required"`

	// Dir is the working directory to run the command in.
	Dir string `yaml:"dir,omitempty" json:"dir,omitempty"`

	// Mounts is the list of sources to mount into the build steps.
	Mounts []SourceMount `yaml:"mounts,omitempty" json:"mounts,omitempty"`

	// List of CacheDirs which will be used across all Steps
	CacheDirs map[string]CacheDirConfig `yaml:"cache_dirs,omitempty" json:"cache_dirs,omitempty"`

	// Env is the list of environment variables to set for all commands in this step group.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`

	// Steps is the list of commands to run to test the package.
	Steps []TestStep `yaml:"steps,omitempty" json:"steps,omitempty"`

	// Files is the list of files to check after running the steps.
	Files map[string]FileCheckOutput `yaml:"files,omitempty" json:"files,omitempty"`
}

// TestStep is a wrapper for [BuildStep] to include checks on stdio streams
type TestStep struct {
	// Command is the command to run to build the artifact(s).
	// This will always be wrapped as /bin/sh -c "<command>", or whatever the equivalent is for the target distro.
	Command string `yaml:"command" json:"command" jsonschema:"required"`
	// Env is the list of environment variables to set for the command.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	// Stdout is the expected output on stdout
	Stdout CheckOutput `yaml:"stdout,omitempty" json:"stdout,omitempty"`
	// Stderr is the expected output on stderr
	Stderr CheckOutput `yaml:"stderr,omitempty" json:"stderr,omitempty"`
	// Stdin is the input to pass to stdin for the command
	Stdin string `yaml:"stdin,omitempty" json:"stdin,omitempty"`
}

// CheckOutput is used to specify the expected output of a check, such as stdout/stderr or a file.
// All non-empty fields will be checked.
type CheckOutput struct {
	// Equals is the exact string to compare the output to.
	Equals string `yaml:"equals,omitempty" json:"equals,omitempty"`
	// Contains is the list of strings to check if they are contained in the output.
	Contains []string `yaml:"contains,omitempty" json:"contains,omitempty"`
	// Matches is the list of regular expressions to match the output against.
	Matches []string `yaml:"matches,omitempty" json:"matches,omitempty"`
	// StartsWith is the string to check if the output starts with.
	StartsWith string `yaml:"starts_with,omitempty" json:"starts_with,omitempty"`
	// EndsWith is the string to check if the output ends with.
	EndsWith string `yaml:"ends_with,omitempty" json:"ends_with,omitempty"`
	// Empty is used to check if the output is empty.
	Empty bool `yaml:"empty,omitempty" json:"empty,omitempty"`
}

// FileCheckOutput is used to specify the expected output of a file.
type FileCheckOutput struct {
	CheckOutput `yaml:",inline"`
	// Permissions is the expected permissions of the file.
	Permissions fs.FileMode `yaml:"permissions,omitempty" json:"permissions,omitempty"`
	// IsDir is used to set the expected file mode to a directory.
	IsDir bool `yaml:"is_dir,omitempty" json:"is_dir,omitempty"`
	// NotExist is used to check that the file does not exist.
	NotExist bool `yaml:"not_exist,omitempty" json:"not_exist,omitempty"`

	// TODO: Support checking symlinks
	// This is not currently possible with buildkit as it does not expose information about the symlink
}

// CheckOutputError is used to build an error message for a failed output check for a test case.
type CheckOutputError struct {
	Kind     string
	Expected string
	Actual   string
	Path     string
}

func (c *CheckOutputError) Error() string {
	return fmt.Sprintf("expected %q %s %q, got %q", c.Path, c.Kind, c.Expected, c.Actual)
}

// IsEmpty is used to determine if there are any checks to perform.
func (c CheckOutput) IsEmpty() bool {
	return c.Equals == "" && len(c.Contains) == 0 && len(c.Matches) == 0 && c.StartsWith == "" && c.EndsWith == "" && !c.Empty
}

func (t *TestSpec) validate() error {
	var errs []error

	for _, m := range t.Mounts {
		if err := m.validate("/"); err != nil {
			errs = append(errs, errors.Wrapf(err, "mount %s", m.Dest))
		}
	}

	for p, cfg := range t.CacheDirs {
		if _, err := sharingMode(cfg.Mode); err != nil {
			errs = append(errs, errors.Wrapf(err, "invalid sharing mode for test %q with cache mount at path %q", t.Name, p))
		}
	}

	return goerrors.Join(errs...)
}

func (c *CheckOutput) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(string) bool) error {
	for i, contains := range c.Contains {
		updated, err := expandArgs(lex, contains, args, allowArg)
		if err != nil {
			return fmt.Errorf("%w: contains at list index %d", err, i)
		}
		c.Contains[i] = updated
	}

	updated, err := expandArgs(lex, c.EndsWith, args, allowArg)
	if err != nil {
		return fmt.Errorf("%w: endsWith", err)
	}
	c.EndsWith = updated

	for i, matches := range c.Matches {
		updated, err = expandArgs(lex, matches, args, allowArg)
		if err != nil {
			return fmt.Errorf("%w: matches at list index %d", err, i)
		}
		c.Matches[i] = updated
	}

	updated, err = expandArgs(lex, c.Equals, args, allowArg)
	if err != nil {
		return fmt.Errorf("%w: equals", err)
	}
	c.Equals = updated

	updated, err = expandArgs(lex, c.StartsWith, args, allowArg)
	if err != nil {
		return fmt.Errorf("%w: startsWith", err)
	}
	c.StartsWith = updated
	return nil
}

func (s *TestStep) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(string) bool) error {
	var errs []error
	appendErr := func(err error) {
		errs = append(errs, err)
	}

	for k, v := range s.Env {
		updated, err := expandArgs(lex, v, args, allowArg)
		if err != nil {
			appendErr(errors.Wrapf(err, "env %s=%s", k, v))
			continue
		}
		s.Env[k] = updated
	}

	updated, err := expandArgs(lex, s.Stdin, args, allowArg)
	if err != nil {
		appendErr(errors.Wrap(err, "stdin"))
	}
	if updated != s.Stdin {
		s.Stdin = updated
	}

	stdout := s.Stdout
	if err := stdout.processBuildArgs(lex, args, allowArg); err != nil {
		appendErr(errors.Wrap(err, "stdout"))
	}
	s.Stdout = stdout

	stderr := s.Stderr
	if err := stderr.processBuildArgs(lex, args, allowArg); err != nil {
		appendErr(errors.Wrap(err, "stderr"))
	}
	s.Stderr = stderr

	return goerrors.Join(errs...)
}

func (c *TestSpec) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(string) bool) error {
	var errs []error
	appendErr := func(err error) {
		errs = append(errs, err)
	}

	for i, s := range c.Mounts {
		if err := s.processBuildArgs(lex, args, allowArg); err != nil {
			appendErr(err)
			continue
		}
		c.Mounts[i] = s
	}

	for k, v := range c.Env {
		updated, err := expandArgs(lex, v, args, allowArg)
		if err != nil {
			appendErr(errors.Wrapf(err, "%s=%s", k, v))
			continue
		}
		c.Env[k] = updated
	}

	for i, step := range c.Steps {
		if err := step.processBuildArgs(lex, args, allowArg); err != nil {
			appendErr(errors.Wrapf(err, "step index %d", i))
			continue
		}
		c.Steps[i] = step
	}

	for name, f := range c.Files {
		if err := f.processBuildArgs(lex, args, allowArg); err != nil {
			appendErr(fmt.Errorf("error performing shell expansion to check output of file %s: %w", name, err))
		}
		c.Files[name] = f
	}

	return errors.Wrap(goerrors.Join(errs...), c.Name)
}

func (c *FileCheckOutput) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(string) bool) error {
	check := c.CheckOutput
	if err := check.processBuildArgs(lex, args, allowArg); err != nil {
		return err
	}
	c.CheckOutput = check
	return nil
}

// Check is used to check the output stream.
func (c CheckOutput) Check(dt string, p string) (retErr error) {
	if c.Empty {
		if dt != "" {
			return &CheckOutputError{Kind: "empty", Expected: "", Actual: dt, Path: p}
		}

		// Anything else would be nonsensical and it would make sense to return early...
		// But we'll check it anyway and it should fail since this would be an invalid CheckOutput
	}

	if c.Equals != "" && c.Equals != dt {
		return &CheckOutputError{Expected: c.Equals, Actual: dt, Path: p}
	}

	for _, contains := range c.Contains {
		if contains != "" && !strings.Contains(dt, contains) {
			return &CheckOutputError{Kind: "contains", Expected: contains, Actual: dt, Path: p}
		}
	}
	for _, matches := range c.Matches {
		regexp, err := regexp.Compile(matches)
		if err != nil {
			return err
		}

		if !regexp.Match([]byte(dt)) {
			return &CheckOutputError{Kind: "matches", Expected: matches, Actual: dt, Path: p}
		}
	}

	if c.StartsWith != "" && !strings.HasPrefix(dt, c.StartsWith) {
		return &CheckOutputError{Kind: "starts_with", Expected: c.StartsWith, Actual: dt, Path: p}
	}

	if c.EndsWith != "" && !strings.HasSuffix(dt, c.EndsWith) {
		return &CheckOutputError{Kind: "ends_with", Expected: c.EndsWith, Actual: dt, Path: p}
	}

	return nil
}

// Check is used to check the output file.
func (c FileCheckOutput) Check(dt string, mode fs.FileMode, isDir bool, p string) error {
	if c.IsDir && !isDir {
		return &CheckOutputError{Kind: "mode", Expected: "ModeDir", Actual: "ModeFile", Path: p}
	}

	if !c.IsDir && isDir {
		return &CheckOutputError{Kind: "mode", Expected: "ModeFile", Actual: "ModeDir", Path: p}
	}

	perm := mode.Perm()
	if c.Permissions != 0 && c.Permissions != perm {
		return &CheckOutputError{Kind: "permissions", Expected: c.Permissions.String(), Actual: perm.String(), Path: p}
	}

	return c.CheckOutput.Check(dt, p)
}
