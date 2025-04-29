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
	"github.com/moby/buildkit/frontend/dockerui"
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

func (src *SourceInline) AsState(name string) (llb.State, error) {
	if src.File != nil {
		return llb.Scratch().With(src.File.PopulateAt(name)), nil
	}
	return llb.Scratch().With(src.Dir.PopulateAt("/")), nil
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

func (src *SourceContext) AsState(path string, includes []string, excludes []string, sOpt SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, error) {
	if !isRoot(path) {
		excludes = append(excludeAllButPath(path), excludes...)
	}

	st, err := sOpt.GetContext(src.Name, localIncludeExcludeMerge(includes, excludes), withFollowPath(path), withConstraints(opts))
	if err != nil {
		return llb.Scratch(), err
	}

	if st == nil {
		return llb.Scratch(), errors.Errorf("context %q not found", src.Name)
	}

	return *st, nil
}

func excludeAllButPath(p string) []string {
	return []string{
		"*",
		"!" + filepath.ToSlash(filepath.Clean(p)),
	}
}

func (src *SourceGit) AsState(opts ...llb.ConstraintsOpt) (llb.State, error) {
	ref, err := gitutil.ParseGitRef(src.URL)
	if err != nil {
		return llb.Scratch(), fmt.Errorf("could not parse git ref: %w", err)
	}

	var gOpts []llb.GitOption
	if src.KeepGitDir {
		gOpts = append(gOpts, llb.KeepGitDir())
	}
	gOpts = append(gOpts, withConstraints(opts))
	gOpts = append(gOpts, src.Auth.LLBOpt())

	st := llb.Git(ref.Remote, src.Commit, gOpts...)
	return st, nil
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

func (src *SourceHTTP) AsState(name string, opts ...llb.ConstraintsOpt) (llb.State, error) {
	httpOpts := []llb.HTTPOption{withConstraints(opts)}
	httpOpts = append(httpOpts, llb.Filename(name))
	if src.Digest != "" {
		httpOpts = append(httpOpts, llb.Checksum(src.Digest))
	}

	if src.Permissions != 0 {
		httpOpts = append(httpOpts, llb.Chmod(src.Permissions))
	}

	st := llb.HTTP(src.URL, httpOpts...)
	return st, nil
}

func (src *SourceHTTP) validate() error {
	if src.URL == "" {
		return errors.New("http source must have a URL")
	}
	if src.Digest != "" {
		if err := src.Digest.Validate(); err != nil {
			return errors.WithStack(err)
		}
	}
	return nil
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
	sourceNamePathSeparatorError = errors.New("source name must not contain path separator")
	errMissingSource             = errors.New("source is missing from sources list")

	errPatchRequiresSubpath = errors.New("patch source refers to a directory source without a subpath to the patch file to use")
	errPatchFileNoSubpath   = errors.New("patch source refers to a file source but patch spec specifies a subpath")
)

type LLBGetter func(sOpts SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, error)

type ForwarderFunc func(llb.State, *SourceBuild, ...llb.ConstraintsOpt) (llb.State, error)

type SourceOpts struct {
	Resolver       llb.ImageMetaResolver
	Forward        ForwarderFunc
	GetContext     func(string, ...llb.LocalOption) (*llb.State, error)
	TargetPlatform *ocispecs.Platform
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

var errInvalidMountConfig = errors.New("invalid mount config")

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

func (m *SourceMount) validate(root string) error {
	if m.Dest == "/" {
		return errors.Wrap(errInvalidMountConfig, "mount destination must not be \"/\"")
	}
	if root != "/" && pathHasPrefix(m.Dest, root) {
		// We cannot support this as the base mount for subPath will shadow the mount being done here.
		return errors.Wrapf(errInvalidMountConfig, "mount destination (%s) must not be a descendent of the target source path (%s)", m.Dest, root)
	}
	return m.Spec.validate()
}

func (m *SourceMount) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(string) bool) error {
	if err := m.Spec.processBuildArgs(lex, args, allowArg); err != nil {
		return errors.Wrapf(err, "mount dest: %s", m.Dest)
	}
	return nil
}

func (m *SourceMount) fillDefaults() {
	src := m.Spec
	fillDefaults(&src)
	m.Spec = src
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

	baseRunOpts := []llb.RunOption{CacheDirsToRunOpt(cmd.CacheDirs, "", "")}

	for _, src := range cmd.Mounts {
		if err := src.validate(subPath); err != nil {
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

		if !SourceIsDir(src.Spec) {
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

func sharingMode(mode string) (llb.CacheMountSharingMode, error) {
	switch mode {
	case "shared", "":
		return llb.CacheMountShared, nil
	case "private":
		return llb.CacheMountPrivate, nil
	case "locked":
		return llb.CacheMountLocked, nil
	default:
		return 0, fmt.Errorf("invalid sharing mode: %s", mode)
	}
}

func WithCreateDestPath() llb.CopyOption {
	return copyOptionFunc(func(i *llb.CopyInfo) {
		i.CreateDestPath = true
	})
}

func SourceIsDir(src Source) bool {
	switch {
	case src.DockerImage != nil,
		src.Git != nil,
		src.Build != nil,
		src.Context != nil:
		return true
	case src.HTTP != nil:
		return false
	case src.Inline != nil:
		return src.Inline.Dir != nil
	default:
		panic("unreachable")
	}
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
		ShArgs("tar -C /src -cvzf /tmp/st ."),
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

func (s *Source) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(key string) bool) error {
	// force the shell lexer to skip unresolved env vars so they aren't
	// replaced with ""
	lex.SkipUnsetEnv = true
	var errs []error
	appendErr := func(err error) {
		errs = append(errs, err)
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

	return goerrors.Join(errs...)
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
