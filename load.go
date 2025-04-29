package dalec

import (
	goerrors "errors"
	"fmt"
	"os"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
	"github.com/pkg/errors"
	"golang.org/x/exp/maps"
)

const (
	// KeyDalecTarget is the key used the build arg name which may be used to read
	// the target name.
	KeyDalecTarget = "DALEC_TARGET"

	parseModeIgnoreComments = 0
)

func knownArg(key string) bool {
	switch key {
	case "BUILDKIT_SYNTAX":
		return true
	case "DALEC_DISABLE_DIFF_MERGE":
		return true
	case "DALEC_SKIP_SIGNING":
		return true
	case "DALEC_SIGNING_CONFIG_CONTEXT_NAME":
		return true
	case "DALEC_SIGNING_CONFIG_PATH":
		return true
	case "SOURCE_DATE_EPOCH":
		return true
	case "DALEC_SKIP_TESTS":
		return true
	case KeyDalecTarget:
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

type envGetterMap map[string]string

func (m envGetterMap) Get(key string) (string, bool) {
	v, ok := m[key]
	return v, ok
}

func (m envGetterMap) Keys() []string {
	return maps.Keys(m)
}

func expandArgs(lex *shell.Lex, s string, args map[string]string, allowArg func(key string) bool) (string, error) {
	result, err := lex.ProcessWordWithMatches(s, envGetterMap(args))
	if err != nil {
		return "", err
	}

	var errs []error
	for m := range result.Unmatched {
		if !knownArg(m) && !allowArg(m) {
			errs = append(errs, fmt.Errorf(`build arg "%s" not declared`, m))
			continue
		}

		if platformArg(m) {
			errs = append(errs, fmt.Errorf(`opt-in arg "%s" not present in args`, m))
		}
	}

	return result.Result, errors.Wrap(goerrors.Join(errs...), "error performing variable expansion")
}

var errUnknownArg = errors.New("unknown arg")

type SubstituteConfig struct {
	AllowArg func(string) bool
}

type SubstituteOpt func(*SubstituteConfig)

// AllowAnyArg can be used to set [SubstituteConfig.AllowArg] to allow any arg
// to be substituted regardless of whether it is declared in the spec.
func AllowAnyArg(s string) bool {
	return true
}

// WithAllowAnyArg is a [SubstituteOpt] that sets [SubstituteConfig.AllowArg] to
// [AllowAnyArg].
func WithAllowAnyArg(cfg *SubstituteConfig) {
	cfg.AllowArg = AllowAnyArg
}

// DisallowAllUndeclared can be used to set [SubstituteConfig.AllowArg] to disallow args
// unless they are declared in the spec.
// This is used by default when substituting args.
func DisallowAllUndeclared(s string) bool {
	return false
}

func (s *Spec) SubstituteArgs(env map[string]string, opts ...SubstituteOpt) error {
	var cfg SubstituteConfig

	cfg.AllowArg = DisallowAllUndeclared

	for _, o := range opts {
		o(&cfg)
	}

	lex := shell.NewLex('\\')
	// force the shell lexer to skip unresolved env vars so they aren't
	// replaced with ""
	lex.SkipUnsetEnv = true

	var errs []error
	appendErr := func(err error) {
		errs = append(errs, err)
	}

	args := make(map[string]string)
	for k, v := range s.Args {
		args[k] = v
	}
	for k, v := range env {
		if _, ok := args[k]; !ok {
			if !knownArg(k) && !cfg.AllowArg(k) {
				appendErr(fmt.Errorf("%w: %q", errUnknownArg, k))
			}

			// if the build arg isn't present in args by opt-in, skip
			// and don't automatically inject a value
			continue
		}

		args[k] = v
	}

	for name, src := range s.Sources {
		if err := src.processBuildArgs(lex, args, cfg.AllowArg); err != nil {
			appendErr(errors.Wrapf(err, "source %q", name))
		}
		s.Sources[name] = src
	}

	updated, err := expandArgs(lex, s.Version, args, cfg.AllowArg)
	if err != nil {
		appendErr(errors.Wrap(err, "version"))
	}
	s.Version = updated

	updated, err = expandArgs(lex, s.Revision, args, cfg.AllowArg)
	if err != nil {
		appendErr(errors.Wrap(err, "revision"))
	}
	s.Revision = updated

	if err := s.Build.processBuildArgs(lex, args, cfg.AllowArg); err != nil {
		appendErr(errors.Wrap(err, "build"))
	}

	if s.Build.NetworkMode != "" {
		updated, err := expandArgs(lex, s.Build.NetworkMode, args, cfg.AllowArg)
		if err != nil {
			appendErr(fmt.Errorf("error performing shell expansion on build network mode: %s: %w", s.Build.NetworkMode, err))
		}
		s.Build.NetworkMode = updated
	}

	for i, step := range s.Build.Steps {
		bs := &step
		if err := bs.processBuildArgs(lex, args, cfg.AllowArg); err != nil {
			appendErr(errors.Wrapf(err, "step index %d", i))
		}
		s.Build.Steps[i] = *bs
	}

	for _, t := range s.Tests {
		if err := t.processBuildArgs(lex, args, cfg.AllowArg); err != nil {
			appendErr(err)
		}
	}

	for name, t := range s.Targets {
		if err := t.processBuildArgs(lex, args, cfg.AllowArg); err != nil {
			appendErr(errors.Wrapf(err, "target %s", name))
		}
		s.Targets[name] = t
	}

	if s.PackageConfig != nil {
		if err := s.PackageConfig.processBuildArgs(lex, args, cfg.AllowArg); err != nil {
			appendErr(errors.Wrap(err, "package config"))
		}
	}

	if err := s.Image.processBuildArgs(lex, args, cfg.AllowArg); err != nil {
		appendErr(errors.Wrap(err, "package config"))
	}

	if err := s.Dependencies.processBuildArgs(lex, args, cfg.AllowArg); err != nil {
		appendErr(errors.Wrap(err, "dependencies"))
	}

	for k, v := range s.Provides {
		for i, ver := range v.Version {
			updated, err := expandArgs(lex, ver, args, cfg.AllowArg)
			if err != nil {
				appendErr(errors.Wrapf(err, "provides %s version %d", k, i))
			}
			s.Provides[k].Version[i] = updated
		}
	}

	for k, v := range s.Replaces {
		for i, ver := range v.Version {
			updated, err := expandArgs(lex, ver, args, cfg.AllowArg)
			if err != nil {
				appendErr(errors.Wrapf(err, "replaces %s version %d", k, i))
			}
			s.Replaces[k].Version[i] = updated
		}
	}

	return goerrors.Join(errs...)
}

// LoadSpec loads a spec from the given data.
func LoadSpec(dt []byte) (*Spec, error) {
	var spec Spec

	if err := yaml.UnmarshalWithOptions(dt, &spec, yaml.Strict()); err != nil {
		return nil, errors.Wrap(err, "error unmarshalling spec")
	}

	if err := spec.Validate(); err != nil {
		return nil, err
	}

	spec.FillDefaults()
	return &spec, nil
}

// rawYAML is similar to json.RawMessage
// We use this to store the raw yaml data for extension fields
type rawYAML []byte

func (y rawYAML) MarshalYAML() ([]byte, error) {
	return y, nil
}

func (y *rawYAML) UnmarshalYAML(dt []byte) error {
	*y = dt
	return nil
}

func (f *extensionFields) UnmarshalYAML(dt []byte) error {
	// We need to store the raw yaml data for each extension key.
	// Parse the yaml, grab all the extension keys and store the raw values in f.

	parsed, err := parser.ParseBytes(dt, parser.ParseComments)
	if err != nil {
		return errors.Wrapf(err, "error parsing yaml: \n%s", string(dt))
	}

	if len(parsed.Docs) != 1 {
		return errors.New("expected exactly one yaml document")
	}

	doc := parsed.Docs[0]
	if doc.Body == nil {
		return nil
	}

	body, ok := doc.Body.(*ast.MappingNode)
	if !ok {
		return errors.Errorf("expected a mapping node, got %T", body)
	}

	var ext extensionFields
	for _, v := range body.Values {
		key := v.Key.String()
		if !strings.HasPrefix(key, "x-") && !strings.HasPrefix(key, "X-") {
			return errors.Errorf("extension mapping key %q must not start with x-", key)
		}

		if ext == nil {
			ext = make(extensionFields)
		}
		ext[key] = rawYAML(v.Value.String())
	}

	if ext != nil {
		*f = ext
	}
	return nil
}

func (s *Spec) UnmarshalYAML(dt []byte) error {
	parsed, err := parser.ParseBytes(dt, parser.ParseComments)
	if err != nil {
		return errors.Wrapf(err, "error parsing yaml: \n%s", string(dt))
	}

	if len(parsed.Docs) != 1 {
		return errors.New("expected exactly one yaml document")
	}

	// Remove extension nodes from the main AST and store them so we can unmarshal
	// them separately.
	body := parsed.Docs[0].Body.(*ast.MappingNode)
	var extNodes []*ast.MappingValueNode
	for i := 0; i < len(body.Values); i++ {
		node := body.Values[i]
		p := node.GetPath()
		if !strings.HasPrefix(p, "$.x-") && !strings.HasPrefix(p, "$.X-") {
			continue
		}

		// Delete the extension node from the AST.
		body.Values = append(body.Values[:i], body.Values[i+1:]...)
		i--
		extNodes = append(extNodes, node)
	}

	parsed, err = parser.ParseBytes([]byte(parsed.String()), parser.ParseComments)
	if err != nil {
		return errors.Wrapf(err, "error parsing yaml: \n%s", parsed.String())
	}

	// Use an internal type to avoid infinite recursion of UnmarshalYAML.
	type internalSpec Spec
	var s2 internalSpec

	dec := yaml.NewDecoder(parsed, yaml.Strict())
	if err := dec.Decode(&s2); err != nil {
		return fmt.Errorf("%w:\n\n%s", errors.Wrap(err, "error unmarshalling parsed document"), parsed.String())
	}

	*s = Spec(s2)

	if len(extNodes) > 0 {
		// Unmarshal all the extension nodes.

		node := ast.Mapping(token.MappingStart("", &token.Position{}), false, extNodes...)
		doc := ast.Document(&token.Token{Position: &token.Position{}}, node)

		var ext extensionFields
		if err := yaml.NewDecoder(doc).Decode(&ext); err != nil {
			return errors.Wrap(err, "error unmarshalling extension nodes")
		}

		s.extensions = ext
		if len(ext) == 0 {
			panic("ext should not be empty")
		}
	}

	return nil
}

func (s Spec) MarshalYAML() ([]byte, error) {
	dtExt, err := yaml.Marshal(s.extensions)
	if err != nil {
		return nil, errors.Wrap(err, "error marshaling extensions")
	}

	parsedExt, err := parser.ParseBytes(dtExt, 0)
	if err != nil {
		return nil, errors.Wrapf(err, "error parsing yaml: \n%s", string(dtExt))
	}

	type internalSpec Spec
	ss := internalSpec(s)

	dt, err := yaml.Marshal(ss)
	if err != nil {
		return nil, errors.Wrap(err, "error marshaling spec")
	}

	parsed, err := parser.ParseBytes(dt, parser.ParseComments)
	if err != nil {
		return nil, errors.Wrap(err, "error re-parsing spec yaml")
	}

	p, err := yaml.PathString("$")
	if err != nil {
		return nil, errors.Wrap(err, "error creating path selector")
	}
	if err := p.MergeFromFile(parsed, parsedExt); err != nil {
		return nil, errors.Wrap(err, "error merging extension nodes")
	}

	return []byte(parsed.String()), nil
}

func (s *BuildStep) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(string) bool) error {
	var errs []error
	for k, v := range s.Env {
		updated, err := expandArgs(lex, v, args, allowArg)
		if err != nil {
			errs = append(errs, errors.Wrapf(err, "env %s=%s", k, v))
		}
		s.Env[k] = updated
	}
	return goerrors.Join(errs...)
}

func (c *Command) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(string) bool) error {
	if c == nil {
		return nil
	}

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
			appendErr(errors.Wrapf(err, "env %s=%v", k, v))
			continue
		}
		c.Env[k] = updated
	}
	for i, step := range c.Steps {
		if err := step.processBuildArgs(lex, args, allowArg); err != nil {
			appendErr(errors.Wrapf(err, "step index %d", i))

		}
		for k, v := range step.Env {
			updated, err := expandArgs(lex, v, args, allowArg)
			if err != nil {
				appendErr(errors.Wrapf(err, "step env %s=%s", k, v))
				continue
			}

			step.Env[k] = updated
			c.Steps[i] = step
		}
	}

	return goerrors.Join(errs...)
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

	s.Dependencies.fillDefaults()
	s.Image.fillDefaults()

	for k := range s.Targets {
		t := s.Targets[k]
		t.fillDefaults()
		s.Targets[k] = t
	}

	s.Image.fillDefaults()
}

func (s Spec) Validate() error {
	var errs []error

	for name, src := range s.Sources {
		if strings.ContainsRune(name, os.PathSeparator) {
			errs = append(errs, &InvalidSourceError{Name: name, Err: sourceNamePathSeparatorError})
		}
		if err := src.validate(); err != nil {
			errs = append(errs, &InvalidSourceError{Name: name, Err: fmt.Errorf("error validating source ref %q: %w", name, err)})
		}

		if src.DockerImage != nil && src.DockerImage.Cmd != nil {
			for p, cfg := range src.DockerImage.Cmd.CacheDirs {
				if _, err := sharingMode(cfg.Mode); err != nil {
					errs = append(errs, &InvalidSourceError{Name: name, Err: errors.Wrapf(err, "invalid sharing mode for source %q with cache mount at path %q", name, p)})
				}
			}
		}
	}

	for _, t := range s.Tests {
		if err := t.validate(); err != nil {
			errs = append(errs, errors.Wrap(err, t.Name))
		}
	}

	for src, patches := range s.Patches {
		for _, patch := range patches {
			patchSrc, ok := s.Sources[patch.Source]
			if !ok {
				errs = append(errs, &InvalidPatchError{Source: src, PatchSpec: &patch, Err: errMissingSource})
				continue
			}

			if err := validatePatch(patch, patchSrc); err != nil {
				errs = append(errs, &InvalidPatchError{Source: src, PatchSpec: &patch, Err: err})
			}
		}
	}

	switch s.Build.NetworkMode {
	case "", netModeNone, netModeSandbox:
	default:
		errs = append(errs, fmt.Errorf("invalid network mode: %q: valid values %s", s.Build.NetworkMode, []string{netModeNone, netModeSandbox}))
	}

	if err := s.Dependencies.validate(); err != nil {
		errs = append(errs, errors.Wrap(err, "dependencies"))
	}

	if err := s.Image.validate(); err != nil {
		errs = append(errs, errors.Wrap(err, "image"))
	}

	for k, t := range s.Targets {
		if err := t.validate(); err != nil {
			errs = append(errs, errors.Wrapf(err, "target %s", k))
		}
	}

	if err := s.Image.validate(); err != nil {
		errs = append(errs, errors.Wrap(err, "image"))
	}

	return goerrors.Join(errs...)
}

func validatePatch(patch PatchSpec, patchSrc Source) error {
	if SourceIsDir(patchSrc) {
		// Patch sources that use directory-backed sources require a subpath in the
		// patch spec.
		if isRoot(patch.Path) {
			return errPatchRequiresSubpath
		}
		return nil
	}

	// File backed sources with a subpath in the patch spec is invalid since it is
	// already a file, not a directory.
	if !isRoot(patch.Path) {
		return errPatchFileNoSubpath
	}
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

func (s *PackageSigner) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(string) bool) error {
	var errs []error
	for k, v := range s.Args {
		updated, err := expandArgs(lex, v, args, allowArg)
		if err != nil {
			errs = append(errs, errors.Wrapf(err, "arg %s=%s", k, v))
			continue
		}
		s.Args[k] = updated
	}
	return goerrors.Join(errs...)
}

func (cfg *PackageConfig) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(string) bool) error {
	if cfg.Signer != nil {
		if err := cfg.Signer.processBuildArgs(lex, args, allowArg); err != nil {
			return errors.Wrap(err, "signer")
		}
	}

	return nil
}

func (b *ArtifactBuild) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(string) bool) error {
	var errs []error

	for k, v := range b.Env {
		updated, err := expandArgs(lex, v, args, allowArg)
		if err != nil {
			errs = append(errs, errors.Wrapf(err, "env %s=%s", k, v))
			continue
		}
		b.Env[k] = updated
	}

	return goerrors.Join(errs...)
}

func validateSymlinks(symlinks map[string]SymlinkTarget) error {
	var (
		errs     []error
		numPairs int
	)

	for oldpath, cfg := range symlinks {
		var err error
		if oldpath == "" {
			err = fmt.Errorf("symlink source is empty")
			errs = append(errs, err)
		}

		if (cfg.Path != "" && len(cfg.Paths) > 0) || (cfg.Path == "" && len(cfg.Paths) == 0) {
			err = fmt.Errorf("'path' and 'paths' fields are mutually exclusive, and at least one is required: "+
				"symlink to %s", oldpath)

			errs = append(errs, err)
		}

		if err != nil {
			continue
		}

		if cfg.Path != "" { // this means .Paths is empty
			numPairs++
			continue
		}

		for _, newpath := range cfg.Paths { // this means .Path is empty
			numPairs++
			if newpath == "" {
				errs = append(errs, fmt.Errorf("symlink newpath should not be empty"))
				continue
			}
		}
	}

	// The remainder of this function checks for duplicate `newpath`s in the
	// symlink pairs. This is not allowed: neither the ordering of the
	// `oldpath` map keys, nor that of the `.Paths` values can be trusted. We
	// also sort both to avoid cache misses, so we would end up with
	// inconsistent behavior -- regardless of whether the inputs are the same.
	if numPairs < 2 {
		return goerrors.Join(errs...)
	}

	var (
		oldpath string
		cfg     SymlinkTarget
	)

	seen := make(map[string]string, numPairs)
	checkDuplicateNewpath := func(newpath string) {
		if newpath == "" {
			return
		}

		if seenPath, found := seen[newpath]; found {
			errs = append(errs, fmt.Errorf("symlink 'newpaths' must be unique: %q points to both %q and %q",
				newpath, oldpath, seenPath))
		}

		seen[newpath] = oldpath
	}

	for oldpath, cfg = range symlinks {
		checkDuplicateNewpath(cfg.Path)

		for _, newpath := range cfg.Paths {
			checkDuplicateNewpath(newpath)
		}
	}

	return goerrors.Join(errs...)
}

func (img *ImageConfig) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(string) bool) error {
	if img == nil {
		return nil
	}

	var errs error

	for k, v := range img.Labels {
		updated, err := expandArgs(lex, v, args, allowArg)
		if err != nil {
			errs = goerrors.Join(errs, errors.Wrapf(err, "env %s=%s", k, v))
			continue
		}
		img.Labels[k] = updated
	}

	return errs
}
