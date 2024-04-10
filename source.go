package dalec

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"path"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/util/gitutil"
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
		src.Inline != nil:
		return filterState(path, src.Includes, src.Excludes, opts...)
	case src.Context != nil:
		// context sources handle includes and excludes
		return filterState(path, []string{}, []string{})
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
		st, err = src.Context.AsState(name, src.Path, src.Includes, src.Excludes, sOpt, opts...)
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

func (src *SourceContext) AsState(name string, srcPath string, includes []string, excludes []string, sOpt SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, error) {
	st, err := sOpt.GetContext(src.Name, LocalIncludeExcludeMerge(includes, excludes), withConstraints(opts))
	if err != nil {
		return llb.Scratch(), err
	}

	if st == nil {
		return llb.Scratch(), errors.Errorf("context %q not found", src.Name)
	}

	return *st, nil
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

	st := llb.Git(ref.Remote, src.Commit, gOpts...)
	return st, nil
	// TODO: Pass git secrets
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

var sourceNamePathSeparatorError = errors.New("source name must not container path separator")

type LLBGetter func(sOpts SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, error)

type ForwarderFunc func(llb.State, *SourceBuild) (llb.State, error)

type SourceOpts struct {
	Resolver   llb.ImageMetaResolver
	Forward    ForwarderFunc
	GetContext func(string, ...llb.LocalOption) (*llb.State, error)
	GetFS      func(st llb.State) fs.ReadDirFS
}

func shArgs(cmd string) llb.RunOption {
	return llb.Args([]string{"sh", "-c", cmd})
}

func handleRename(s *Source, name string, sOpt SourceOpts) llb.StateOption {
	rename := func(st llb.State) llb.State {
		stFS := sOpt.GetFS(st)
		entries, err := stFS.ReadDir("/")

		// we are not expecting an error here
		if err != nil {
			panic(err)
		}

		if len(entries) == 1 && !entries[0].IsDir() {
			// we have a single file at the root of the source, we need to rename it
			srcName := path.Join("/", entries[0].Name())
			destName := path.Join("/", name)
			return llb.Scratch().File(llb.Copy(st, srcName, destName))
		}

		return st
	}
	noRename := func(st llb.State) llb.State { return st }

	if s.Context != nil && s.Path != "" {
		return rename
	}

	return noRename
}

func (s *Source) asState(name string, forMount bool, sOpt SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, bool, error) {
	st, err := getSource(*s, name, sOpt, opts...)
	if err != nil {
		return llb.Scratch(), false, err
	}
	isDir := s.IsDir(st, sOpt)
	sourceState := st.With(getFilter(name, *s, forMount)).
		With(handleRename(s, name, sOpt)) // certain sources such as source context require an extra
		// step to rename the contents to the source name if the contents
		// are a single file

	return sourceState, isDir, nil
}

// returns llb.State of Source, bool for IsDirectory, and error
func (s *Source) AsState(name string, sOpt SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, bool, error) {
	return s.asState(name, false, sOpt, opts...)
}

// returns llb.State of Source, bool for IsDirectory, and error
func (s *Source) AsMount(name string, sOpt SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, bool, error) {
	return s.asState(name, true, sOpt, opts...)
}

// must not be called with a nil cmd pointer
// subPath must be a valid non-empty path
func generateSourceFromImage(st llb.State, cmd *Command, sOpts SourceOpts, subPath string, opts ...llb.ConstraintsOpt) (llb.State, error) {
	if len(cmd.Steps) == 0 {
		return llb.Scratch(), fmt.Errorf("no steps defined for image source")
	}

	for k, v := range cmd.Env {
		st = st.AddEnv(k, v)
	}
	if cmd.Dir != "" {
		st = st.Dir(cmd.Dir)
	}

	baseRunOpts := []llb.RunOption{CacheDirsToRunOpt(cmd.CacheDirs, "", "")}

	for _, src := range cmd.Mounts {
		srcSt, _, err := src.Spec.AsMount(src.Dest, sOpts, opts...)
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

		if !SourceIsDir(src.Spec, srcSt, sOpts) {
			mountOpt = append(mountOpt, llb.SourcePath(src.Dest))
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

func (s *Source) IsDir(srcState llb.State, sOpt SourceOpts) bool {
	return SourceIsDir(*s, srcState, sOpt)
}

func isDir(inPath string, fs fs.ReadDirFS) bool {
	pathDir := path.Dir(inPath)
	baseName := path.Base(inPath)
	entries, err := fs.ReadDir(pathDir)
	if err != nil {
		panic(err)
	}
	for _, e := range entries {
		if e.Name() == baseName {
			return e.IsDir()
		}
	}
	panic("need to handle") // could not exist?
}

func isContextDir(src Source, srcState llb.State, sOpt SourceOpts) bool {
	srcFS := sOpt.GetFS(srcState)
	if src.Path != "" {
		p := path.Join("/", src.Path)
		return isDir(p, srcFS)
	}
	// return true
	entries, err := srcFS.ReadDir("/")
	if err != nil {
		panic(err)
	}
	if len(entries) == 1 {
		return entries[0].IsDir()
	} else if len(entries) == 0 {
		panic("shouldn't be the case")
	}
	return true
}

func SourceIsDir(src Source, srcState llb.State, sOpt SourceOpts) bool {
	switch {
	case src.Context != nil:
		//return true
		return isContextDir(src, srcState, sOpt)
	case src.DockerImage != nil,
		src.Git != nil,
		src.Build != nil:
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
			fmt.Fprintln(b, "	Extraced path:", s.Path)
		}
	case s.DockerImage != nil:
		img := s.DockerImage
		if img.Cmd == nil {
			fmt.Fprintln(b, "Generated from a docker image:")
			fmt.Fprintln(b, "	Image:", img.Ref)
			if s.Path != "" {
				fmt.Fprintln(b, "	Extraced path:", s.Path)
			}
		} else {
			fmt.Fprintln(b, "Generated from running a command(s) in a docker image:")
			fmt.Fprintln(b, "	Image:", img.Ref)
			if s.Path != "" {
				fmt.Fprintln(b, "	Extraced path:", s.Path)
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
		sourceState = worker.Run(
			llb.AddMount("/patch", patchState, llb.Readonly, llb.SourcePath(p.Source)),
			llb.Dir("src"),
			shArgs(fmt.Sprintf("patch -p%d < /patch", *p.Strip)),
			WithConstraints(opts...),
		).AddMount("/src", sourceState)
	}

	return sourceState
}

// PatchSources returns a new map containing the patched LLB state for each source in the source map.
// Sources that are not patched are also included in the result for convienence.
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

		st, _, err := src.AsState(name, sOpt, opts...)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get source state for %q", name)
		}

		states[name] = st
		for _, p := range s.Patches[name] {
			src, ok := s.Sources[p.Source]
			if !ok {
				return nil, errors.Errorf("patch source %q not found", p.Source)
			}

			states[p.Source], _, err = src.AsState(p.Source, sOpt, opts...)
			if err != nil {
				return nil, errors.Wrapf(err, "failed to get patch source state for %q", p.Source)
			}
		}
	}

	return PatchSources(worker, s, states, opts...), nil
}
