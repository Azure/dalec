//go:generate go run ./cmd/gen-source-variants source_generated.go
package dalec

import (
	"bufio"
	"bytes"
	"encoding/json"
	goerrors "errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/util/gitutil"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

type FilterFunc = func(string, []string, []string, ...llb.ConstraintsOpt) llb.StateOption

var errNoSourceVariant = fmt.Errorf("no source variant found")

func (src Source) handlesOwnPath() bool {
	// docker images handle their own path extraction if they have an attached command,
	// and this information is needed in the case of mounts when we can do path
	// extraction at mount time
	return src.DockerImage != nil && src.DockerImage.Cmd != nil
}

func getFilter(src Source, forMount bool, opts ...llb.ConstraintsOpt) llb.StateOption {
	var path = src.Path
	if forMount {
		// if we're using a mount for these sources, the mount will handle path extraction
		path = "/"
	}

	switch {
	case src.HTTP != nil,
		src.Git != nil,
		src.Build != nil,
		src.Context != nil,
		src.Inline != nil:
		return filterState(path, src.Includes, src.Excludes, opts...)
	case src.DockerImage != nil:
		if src.DockerImage.Cmd != nil {
			// if a docker image source has a command,
			// the path extraction will be handled with a mount on the command
			path = "/"
		}

		return filterState(path, src.Includes, src.Excludes)
	}

	return func(st llb.State) llb.State { return st }
}

func getSource(src Source, name string, sOpt SourceOpts, opts ...llb.ConstraintsOpt) (st llb.State, err error) {
	// load the source
	switch {
	case src.HTTP != nil:
		st, err = src.HTTP.AsState(name, opts...)
	case src.Git != nil:
		st, err = src.Git.AsState(opts...)
	case src.Context != nil:
		st, err = src.Context.AsState(src.Path, src.Includes, src.Excludes, sOpt, opts...)
	case src.DockerImage != nil:
		st, err = src.DockerImage.AsState(name, src.Path, sOpt, opts...)
	case src.Build != nil:
		st, err = src.Build.AsState(name, sOpt, opts...)
	case src.Inline != nil:
		st, err = src.Inline.AsState(name)
	default:
		st, err = llb.Scratch(), errNoSourceVariant
	}

	return
}

// withFollowPath similar to using [llb.IncludePatterns] except that it will
// follow symlinks at the provided path.
func withFollowPath(p string) localOptionFunc {
	return func(li *llb.LocalInfo) {
		if isRoot(p) {
			return
		}

		paths := []string{p}
		if li.FollowPaths != "" {
			var ls []string
			if err := json.Unmarshal([]byte(li.FollowPaths), &ls); err != nil {
				panic(err)
			}
			paths = append(ls, paths...)
		}
		llb.FollowPaths(paths).SetLocalOption(li)
	}
}

func excludeAllButPath(p string) []string {
	return []string{
		"*",
		"!" + filepath.ToSlash(filepath.Clean(p)),
	}
}

// InvalidSourceError is an error type returned when a source is invalid.
type InvalidSourceError struct {
	Name string
	Err  error
}

func (s *InvalidSourceError) Error() string {
	return fmt.Sprintf("invalid source %s: %v", s.Name, s.Err)
}

func (s *InvalidSourceError) Unwrap() error {
	return s.Err
}

type InvalidPatchError struct {
	Source    string
	PatchSpec *PatchSpec
	Err       error
}

func (s *InvalidPatchError) Error() string {
	return fmt.Sprintf("invalid patch for source %q, patch source: %q: %v", s.Source, s.PatchSpec.Source, s.Err)
}

func (s *InvalidPatchError) Unwrap() error {
	return s.Err
}

var (
	errSourceNamePathSeparator = goerrors.New("source name must not contain path separator")
	errMissingSource           = goerrors.New("source is missing from sources list")

	errPatchRequiresSubpath = goerrors.New("patch source refers to a directory source without a subpath to the patch file to use")
	errPatchFileNoSubpath   = goerrors.New("patch source refers to a file source but patch spec specifies a subpath")
)

type LLBGetter func(sOpts SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, error)

type ForwarderFunc func(llb.State, *SourceBuild, ...llb.ConstraintsOpt) (llb.State, error)

type SourceOpts struct {
	Resolver         llb.ImageMetaResolver
	Forward          ForwarderFunc
	GetContext       func(string, ...llb.LocalOption) (*llb.State, error)
	TargetPlatform   *ocispecs.Platform
	GitCredHelperOpt func() (llb.RunOption, error)
}

func (s *Source) asState(name string, forMount bool, sOpt SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, error) {
	st, err := getSource(*s, name, sOpt, opts...)
	if err != nil {
		return llb.Scratch(), err
	}

	return st.With(getFilter(*s, forMount)), nil
}

func (s *Source) AsState(name string, sOpt SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, error) {
	return s.asState(name, false, sOpt, opts...)
}

func (s *Source) AsMount(name string, sOpt SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, error) {
	return s.asState(name, true, sOpt, opts...)
}

var errInvalidMountConfig = goerrors.New("invalid mount config")

func pathHasPrefix(s string, prefix string) bool {
	if s == prefix {
		return true
	}

	s = filepath.Clean(s)
	prefix = filepath.Clean(prefix)

	if s == prefix {
		return true
	}

	if strings.HasPrefix(s, prefix+"/") {
		return true
	}
	return false
}

func isRoot(extract string) bool {
	return extract == "" || extract == "/" || extract == "."
}

func filterState(extract string, includes, excludes []string, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(st llb.State) llb.State {
		// if we have no includes, no excludes, and no non-root source path,
		// then this is a no-op
		if len(includes) == 0 && len(excludes) == 0 && isRoot(extract) {
			return st
		}

		filtered := llb.Scratch().File(
			llb.Copy(
				st,
				extract,
				"/",
				WithIncludes(includes),
				WithExcludes(excludes),
				WithDirContentsOnly(),
			),
			withConstraints(opts),
		)

		return filtered
	}
}

func WithCreateDestPath() llb.CopyOption {
	return copyOptionFunc(func(i *llb.CopyInfo) {
		i.CreateDestPath = true
	})
}

func SourceIsDir(src Source) bool {
	return src.IsDir()
}

func (src *Source) GetDisplayRef() (string, error) {
	s := ""
	switch {
	case src.DockerImage != nil:
		s = src.DockerImage.Ref
	case src.Git != nil:
		s = src.Git.URL
	case src.HTTP != nil:
		s = src.HTTP.URL
	case src.Context != nil:
		s = src.Context.Name
	case src.Build != nil:
		s = fmt.Sprintf("%v", src.Build.Source)
	case src.Inline != nil:
		s = "inline"
	default:
		return "", fmt.Errorf("no non-nil source provided")
	}

	return s, nil
}

// Doc returns the details of how the source was created.
// This should be included, where applicable, in build in build specs (such as RPM spec files)
// so that others can reproduce the build.
func (s Source) Doc(name string) (io.Reader, error) {
	b := bytes.NewBuffer(nil)
	switch {
	case s.Context != nil:
		fmt.Fprintln(b, "Generated from a local docker build context and is unreproducible.")
	case s.Build != nil:
		fmt.Fprintln(b, "Generated from a docker build:")
		fmt.Fprintln(b, "	Docker Build Target:", s.Build.Target)
		sub, err := s.Build.Source.Doc(name)
		if err != nil {
			return nil, err
		}

		scanner := bufio.NewScanner(sub)
		for scanner.Scan() {
			fmt.Fprintf(b, "			%s\n", scanner.Text())
		}
		if scanner.Err() != nil {
			return nil, scanner.Err()
		}

		if len(s.Build.Args) > 0 {
			sorted := SortMapKeys(s.Build.Args)
			fmt.Fprintln(b, "	Build Args:")
			for _, k := range sorted {
				fmt.Fprintf(b, "		%s=%s\n", k, s.Build.Args[k])
			}
		}

		p := "Dockerfile"
		if s.Build.DockerfilePath != "" {
			p = s.Build.DockerfilePath
		}
		fmt.Fprintln(b, "	Dockerfile path in context:", p)
	case s.HTTP != nil:
		fmt.Fprintln(b, "Generated from a http(s) source:")
		fmt.Fprintln(b, "	URL:", s.HTTP.URL)
	case s.Git != nil:
		git := s.Git
		ref, err := gitutil.ParseGitRef(git.URL)
		if err != nil {
			return nil, err
		}
		fmt.Fprintln(b, "Generated from a git repository:")
		fmt.Fprintln(b, "	Remote:", ref.Remote)
		fmt.Fprintln(b, "	Ref:", git.Commit)
		if s.Path != "" {
			fmt.Fprintln(b, "	Extracted path:", s.Path)
		}
	case s.DockerImage != nil:
		img := s.DockerImage
		if img.Cmd == nil {
			fmt.Fprintln(b, "Generated from a docker image:")
			fmt.Fprintln(b, "	Image:", img.Ref)
			if s.Path != "" {
				fmt.Fprintln(b, "	Extracted path:", s.Path)
			}
		} else {
			fmt.Fprintln(b, "Generated from running a command(s) in a docker image:")
			fmt.Fprintln(b, "	Image:", img.Ref)
			if s.Path != "" {
				fmt.Fprintln(b, "	Extracted path:", s.Path)
			}
			if len(img.Cmd.Env) > 0 {
				fmt.Fprintln(b, "	With the following environment variables set for all commands:")

				sorted := SortMapKeys(img.Cmd.Env)
				for _, k := range sorted {
					fmt.Fprintf(b, "		%s=%s\n", k, img.Cmd.Env[k])
				}
			}
			if img.Cmd.Dir != "" {
				fmt.Fprintln(b, "	Working Directory:", img.Cmd.Dir)
			}
			fmt.Fprintln(b, "	Command(s):")
			for _, step := range img.Cmd.Steps {
				fmt.Fprintf(b, "		%s\n", step.Command)
				if len(step.Env) > 0 {
					fmt.Fprintln(b, "			With the following environment variables set for this command:")
					sorted := SortMapKeys(step.Env)
					for _, k := range sorted {
						fmt.Fprintf(b, "				%s=%s\n", k, step.Env[k])
					}
				}
			}
			if len(img.Cmd.Mounts) > 0 {
				fmt.Fprintln(b, "	With the following items mounted:")
				for _, src := range img.Cmd.Mounts {
					sub, err := src.Spec.Doc(name)
					if err != nil {
						return nil, err
					}

					fmt.Fprintln(b, "		Destination Path:", src.Dest)
					scanner := bufio.NewScanner(sub)
					for scanner.Scan() {
						fmt.Fprintf(b, "			%s\n", scanner.Text())
					}
					if scanner.Err() != nil {
						return nil, scanner.Err()
					}
				}
			}
			return b, nil
		}
	case s.Inline != nil:
		fmt.Fprintln(b, "Generated from an inline source:")
		s.Inline.Doc(b, name)
	default:
		// This should be unrecable.
		// We could panic here, but ultimately this is just a doc string and parsing user generated content.
		fmt.Fprintln(b, "Generated from an unknown source type")
	}

	return b, nil
}

func patchSource(worker, sourceState llb.State, sourceToState map[string]llb.State, patchNames []PatchSpec, opts ...llb.ConstraintsOpt) llb.State {
	for _, p := range patchNames {
		patchState := sourceToState[p.Source]
		// on each iteration, mount source state to /src to run `patch`, and
		// set the state under /src to be the source state for the next iteration

		subPath := p.Source
		if p.Path != "" {
			subPath = p.Path
		}

		sourceState = worker.Run(
			llb.AddMount("/patch", patchState, llb.Readonly, llb.SourcePath(subPath)),
			llb.Dir("src"),
			ShArgs(fmt.Sprintf("patch -p%d < /patch", *p.Strip)),
			WithConstraints(opts...),
		).AddMount("/src", sourceState)
	}

	return sourceState
}

// PatchSources returns a new map containing the patched LLB state for each source in the source map.
// Sources that are not patched are also included in the result for convenience.
// `sourceToState` must be a complete map from source name -> llb state for each source in the dalec spec.
// `worker` must be an LLB state with a `patch` binary present.
func PatchSources(worker llb.State, spec *Spec, sourceToState map[string]llb.State, opts ...llb.ConstraintsOpt) map[string]llb.State {
	// duplicate map to avoid possibly confusing behavior of mutating caller's map
	states := DuplicateMap(sourceToState)
	pgID := identity.NewID()
	sorted := SortMapKeys(states)

	for _, sourceName := range sorted {
		sourceState := states[sourceName]

		patches, patchesExist := spec.Patches[sourceName]
		if !patchesExist {
			continue
		}
		pg := llb.ProgressGroup(pgID, "Patch spec source: "+sourceName+" ", false)
		states[sourceName] = patchSource(worker, sourceState, states, patches, pg, withConstraints(opts))
	}

	return states
}

func (s *Spec) getPatchedSources(sOpt SourceOpts, worker llb.State, filterFunc func(string) bool, opts ...llb.ConstraintsOpt) (map[string]llb.State, error) {
	states := map[string]llb.State{}
	for name, src := range s.Sources {
		if !filterFunc(name) {
			continue
		}

		st, err := src.AsState(name, sOpt, opts...)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get source state for %q", name)
		}

		states[name] = st
		for _, p := range s.Patches[name] {
			src, ok := s.Sources[p.Source]
			if !ok {
				return nil, errors.Errorf("patch source %q not found", p.Source)
			}

			states[p.Source], err = src.AsState(p.Source, sOpt, opts...)
			if err != nil {
				return nil, errors.Wrapf(err, "failed to get patch source state for %q", p.Source)
			}
		}
	}

	return PatchSources(worker, s, states, opts...), nil
}

// Tar creates a tar+gz from the provided state and puts it in the provided dest.
// The provided work state is used to perform the necessary operations to produce the tarball and requires the tar and gzip binaries.
func Tar(work llb.State, src llb.State, dest string, opts ...llb.ConstraintsOpt) llb.State {
	// Put the output tar in a consistent location regardless of `dest`
	// This way if `dest` changes we don't have to rebuild the tarball, which can be expensive.
	outBase := "/tmp/out"
	out := filepath.Join(outBase, filepath.Dir(dest))
	worker := work.Run(
		llb.AddMount("/src", src, llb.Readonly),
		ShArgs("tar -C /src -czf /tmp/st ."),
		WithConstraints(opts...),
	).
		Run(
			llb.Args([]string{"/bin/sh", "-c", "mkdir -p " + out + " && mv /tmp/st " + filepath.Join(out, filepath.Base(dest))}),
			WithConstraints(opts...),
		)

	return worker.AddMount(outBase, llb.Scratch())
}

func DefaultTarWorker(resolver llb.ImageMetaResolver, opts ...llb.ConstraintsOpt) llb.State {
	return llb.Image("busybox:latest", llb.WithMetaResolver(resolver), withConstraints(opts))
}

// Sources gets all the source LLB states from the spec.
func Sources(spec *Spec, sOpt SourceOpts, opts ...llb.ConstraintsOpt) (map[string]llb.State, error) {
	states := make(map[string]llb.State, len(spec.Sources))

	for k, src := range spec.Sources {
		pg := ProgressGroup("Prepare source: " + k)
		opts := append(opts, pg)

		st, err := src.AsState(k, sOpt, opts...)
		if err != nil {
			return nil, errors.Wrapf(err, "could not get source stat e for source: %s", k)
		}

		states[k] = st
	}
	return states, nil
}

func (g *SourceGenerator) fillDefaults(host string, authInfo *GitAuth) {
	if g == nil || authInfo == nil {
		return
	}

	switch {
	case g.Gomod != nil:
		g.Gomod.fillDefaults(host, authInfo)
	}
}

func (gm *GeneratorGomod) fillDefaults(host string, authInfo *GitAuth) {
	// Don't overwrite explicitly-specified auth
	_, ok := gm.Auth[host]
	if ok {
		return
	}
	const defaultUsername = "git"

	var gomodAuth GomodGitAuth
	switch {
	case authInfo.Token != "":
		gomodAuth.Token = authInfo.Token
	case authInfo.Header != "":
		gomodAuth.Header = authInfo.Header
	case authInfo.SSH != "":
		gomodAuth.SSH = &GomodGitAuthSSH{
			ID:       authInfo.SSH,
			Username: defaultUsername,
		}
	default:
		return
	}

	if gm.Auth == nil {
		gm.Auth = make(map[string]GomodGitAuth)
	}

	gm.Auth[host] = gomodAuth
}

func (s *Source) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(key string) bool) error {
	// force the shell lexer to skip unresolved env vars so they aren't
	// replaced with ""
	lex.SkipUnsetEnv = true
	var errs []error
	appendErr := func(err error) {
		errs = append(errs, err)
	}

	if s.Path != "" {
		updated, err := expandArgs(lex, s.Path, args, allowArg)
		if err != nil {
			appendErr(err)
		} else {
			s.Path = updated
		}
	}

	for i, g := range s.Includes {
		updated, err := expandArgs(lex, g, args, allowArg)
		if err != nil {
			appendErr(err)
			continue
		}
		s.Includes[i] = updated
	}

	for i, g := range s.Excludes {
		updated, err := expandArgs(lex, g, args, allowArg)
		if err != nil {
			appendErr(err)
			continue
		}
		s.Excludes[i] = updated
	}

	switch {
	case s.DockerImage != nil:
		updated, err := expandArgs(lex, s.DockerImage.Ref, args, allowArg)
		if err != nil {
			appendErr(fmt.Errorf("image ref: %w", err))
		}
		s.DockerImage.Ref = updated

		if s.DockerImage.Cmd != nil {
			if err := s.DockerImage.Cmd.processBuildArgs(lex, args, allowArg); err != nil {
				appendErr(errors.Wrap(err, "docker image cmd source"))
			}
		}
	case s.Git != nil:
		updated, err := expandArgs(lex, s.Git.URL, args, allowArg)
		s.Git.URL = updated
		if err != nil {
			appendErr(err)
		}

		updated, err = expandArgs(lex, s.Git.Commit, args, allowArg)
		s.Git.Commit = updated
		if err != nil {
			appendErr(err)
		}

	case s.HTTP != nil:
		updated, err := expandArgs(lex, s.HTTP.URL, args, allowArg)
		if err != nil {
			appendErr(err)
		}
		s.HTTP.URL = updated
	case s.Context != nil:
		updated, err := expandArgs(lex, s.Context.Name, args, allowArg)
		s.Context.Name = updated
		if err != nil {
			appendErr(err)
		}
	case s.Build != nil:
		err := s.Build.Source.processBuildArgs(lex, args, allowArg)
		if err != nil {
			appendErr(err)
		}

		updated, err := expandArgs(lex, s.Build.DockerfilePath, args, allowArg)
		if err != nil {
			appendErr(err)
		}
		s.Build.DockerfilePath = updated

		updated, err = expandArgs(lex, s.Build.Target, args, allowArg)
		if err != nil {
			appendErr(err)
		}
		s.Build.Target = updated
	}

	for _, gen := range s.Generate {
		if err := gen.processBuildArgs(args, allowArg); err != nil {
			errs = append(errs, err)
		}
	}

	return goerrors.Join(errs...)
}

func (g *SourceGenerator) processBuildArgs(args map[string]string, allowArg func(key string) bool) error {
	var errs []error

	if g.Gomod != nil {
		if err := g.Gomod.processBuildArgs(args, allowArg); err != nil {
			errs = append(errs, err)
		}
	}

	return goerrors.Join(errs...)
}

func (g *GeneratorGomod) processBuildArgs(args map[string]string, allowArg func(key string) bool) error {
	var errs []error
	lex := shell.NewLex('\\')
	// force the shell lexer to skip unresolved env vars so they aren't
	// replaced with ""
	lex.SkipUnsetEnv = true

	for host, auth := range g.Auth {
		subbed, err := expandArgs(lex, host, args, allowArg)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		g.Auth[subbed] = auth
		if subbed != host {
			delete(g.Auth, host)
		}
	}

	return goerrors.Join(errs...)
}

func (s *Source) validate() error {
	var errs []error

	for i, g := range s.Generate {
		if err := g.Validate(); err != nil {
			errs = append(errs, errors.Wrapf(err, "source generator %d", i))
		}
	}

	var invalid bool
	if err := s.validateSourceVariants(); err != nil {
		invalid = true
		errs = append(errs, err)
	}

	if !invalid {
		// Only validate the source if it is a valid source variant so as to avoid panics.
		if err := s.toInterface().validate(s.fetchOptions()); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return goerrors.Join(errs...)
	}

	return nil
}

type source interface {
	toState(fetchOptions) llb.State
	IsDir() bool
	validate(fetchOptions) error
	toMount(to string, opt fetchOptions, mountOpts ...llb.MountOption) llb.RunOption
	fillDefaults()
}

type fetchOptions struct {
	Constraints []llb.ConstraintsOpt
	Path        string
	Includes    []string
	Excludes    []string
	Rename      string
	SourceOpt   SourceOpts
}

func (s *Source) fetchOptions() fetchOptions {
	return fetchOptions{
		Includes: s.Includes,
		Excludes: s.Excludes,
		Path:     s.Path,
	}
}

func (s *Source) toIntercace() source {
	return s.toInterface()
}

func (s *Source) ToState(name string, opts ...llb.ConstraintsOpt) llb.State {
	fo := s.fetchOptions()
	fo.Constraints = opts
	fo.Rename = name
	st := s.toIntercace().toState(fo)
	return st
}

func (s *Source) ToMount(to string, c llb.ConstraintsOpt, opts ...llb.MountOption) llb.RunOption {
	fo := s.fetchOptions()
	fo.Constraints = append(fo.Constraints, c)
	return s.toIntercace().toMount(to, fo, opts...)
}

func (s *Source) IsDir() bool {
	return s.toIntercace().IsDir()
}

func sourceFilters(opts fetchOptions) llb.StateOption {
	return func(st llb.State) llb.State {
		if len(opts.Excludes) == 0 && len(opts.Includes) == 0 && isRoot(opts.Path) {
			return st
		}
		return llb.Scratch().File(llb.Copy(st, opts.Path, opts.Rename, opts), opts.Constraints...)
	}
}

// SetCopyOptions is an llb.CopyOption that sets the includes and excludes for a copy operation.
func (opts fetchOptions) SetCopyOption(info *llb.CopyInfo) {
	if len(opts.Includes) > 0 {
		WithIncludes(opts.Includes).SetCopyOption(info)
	}
	if len(opts.Excludes) > 0 {
		WithExcludes(opts.Excludes).SetCopyOption(info)
	}
}

// SetLocalOption is an llb.LocalOption that sets various options needed for local (or context) backed sources.
func (opts fetchOptions) SetLocalOption(info *llb.LocalInfo) {
	includes := opts.Includes
	excludes := opts.Excludes
	isRoot := isRoot(opts.Path)

	// For all include/exclude patterns, we need to prepend the base path
	// since the dalec spec assumes that include/excludes are relative to the requested source path.
	// Since we are relying on the underlying LLB implementation to handle the filtering and not requesting
	// more data from the client than it should, we need to ensure that the paths are correct.

	if len(opts.Excludes) > 0 && !isRoot {
		excludes = make([]string, len(opts.Excludes))
		for i, exclude := range opts.Excludes {
			excludes[i] = filepath.Join(opts.Path, exclude)
		}
	}

	if len(opts.Includes) > 0 && !isRoot {
		includes = make([]string, len(opts.Includes))
		for i, include := range opts.Includes {
			includes[i] = filepath.Join(opts.Path, include)
		}
	}

	if isRoot {
		// Exclude anything that is not underneath the requested path
		// This way we aren't needlessly copying data that is not needed.
		excludes = append(excludeAllButPath(opts.Path), excludes...)
	}

	localIncludeExcludeMerge(includes, excludes).SetLocalOption(info)
	if len(opts.Constraints) > 0 {
		WithConstraints(opts.Constraints...).SetLocalOption(info)
	}

	withFollowPath(opts.Path).SetLocalOption(info)
}

func mountFilters(opts fetchOptions) llb.StateOption {
	// Here we don't want the normal source filters because this is going to be mounted, we can filter
	// down to the requested path as part of a mount.
	//
	// We also don't need to rename anything since we are mounting it to a specific path.
	//
	// We do however need to handle any includes/excludes that are set so that we don't have more data
	// than expected in the mount.
	return func(st llb.State) llb.State {
		return llb.Scratch().File(llb.Copy(st, "/", "/", opts), opts.Constraints...)
	}
}

func (s *Source) fillDefaults() {
	s.toIntercace().fillDefaults()
}
