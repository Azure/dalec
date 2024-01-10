package dalec

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerui"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/util/gitutil"
)

type LLBGetter func(sOpts SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, error)

type ForwarderFunc func(llb.State, *SourceBuild) (llb.State, error)

type SourceOpts struct {
	Resolver   llb.ImageMetaResolver
	Forward    ForwarderFunc
	GetContext func(string, ...llb.LocalOption) (*llb.State, error)
}

func shArgs(cmd string) llb.RunOption {
	return llb.Args([]string{"sh", "-c", cmd})
}

// must not be called with a nil cmd pointer
func generateSourceFromImage(s *Spec, name string, st llb.State, cmd *Command, sOpts SourceOpts, opts ...llb.ConstraintsOpt) (llb.ExecState, error) {
	var zero llb.ExecState
	if len(cmd.Steps) == 0 {
		return zero, fmt.Errorf("no steps defined for image source")
	}
	for k, v := range cmd.Env {
		st = st.AddEnv(k, v)
	}
	if cmd.Dir != "" {
		st = st.Dir(cmd.Dir)
	}

	baseRunOpts := []llb.RunOption{CacheDirsToRunOpt(cmd.CacheDirs, "", "")}

	for _, src := range cmd.Mounts {
		srcSt, err := source2LLBGetter(s, src.Spec, name, true)(sOpts, opts...)
		if err != nil {
			return zero, err
		}
		var mountOpt []llb.MountOption
		if src.Spec.Path != "" && len(src.Spec.Includes) == 0 && len(src.Spec.Excludes) == 0 {
			mountOpt = append(mountOpt, llb.SourcePath(src.Spec.Path))
		}
		baseRunOpts = append(baseRunOpts, llb.AddMount(src.Dest, srcSt, mountOpt...))
	}

	var cmdSt llb.ExecState
	for _, step := range cmd.Steps {
		rOpts := []llb.RunOption{llb.Args([]string{
			"/bin/sh", "-c", step.Command,
		})}

		rOpts = append(rOpts, baseRunOpts...)

		for k, v := range step.Env {
			rOpts = append(rOpts, llb.AddEnv(k, v))
		}

		rOpts = append(rOpts, withConstraints(opts))
		cmdSt = st.Run(rOpts...)
	}

	return cmdSt, nil
}

func Source2LLBGetter(s *Spec, src Source, name string) LLBGetter {
	return source2LLBGetter(s, src, name, false)
}

func needsFilter(o *filterOpts) bool {
	if o.source.Path != "" && !o.forMount && !o.pathHandled {
		return true
	}
	if o.includeExcludeHandled {
		return false
	}
	if len(o.source.Includes) > 0 || len(o.source.Excludes) > 0 {
		return true
	}
	return false
}

type filterOpts struct {
	state                 llb.State
	source                Source
	opts                  []llb.ConstraintsOpt
	forMount              bool
	includeExcludeHandled bool
	pathHandled           bool
	err                   error
}

func handleFilter(o *filterOpts) (llb.State, error) {
	if o.err != nil {
		return o.state, o.err
	}

	if !needsFilter(o) {
		return o.state, nil
	}

	srcPath := "/"
	if !o.pathHandled {
		srcPath = o.source.Path
	}

	filtered := llb.Scratch().File(
		llb.Copy(
			o.state,
			srcPath,
			"/",
			WithIncludes(o.source.Includes),
			WithExcludes(o.source.Excludes),
			WithDirContentsOnly(),
		),
		withConstraints(o.opts),
	)

	return filtered, nil
}

func source2LLBGetter(s *Spec, src Source, name string, forMount bool) LLBGetter {
	return func(sOpt SourceOpts, opts ...llb.ConstraintsOpt) (ret llb.State, retErr error) {
		var (
			includeExcludeHandled bool
			pathHandled           bool
		)

		defer func() {
			ret, retErr = handleFilter(&filterOpts{
				state:                 ret,
				source:                src,
				opts:                  opts,
				forMount:              forMount,
				includeExcludeHandled: includeExcludeHandled,
				pathHandled:           pathHandled,
				err:                   retErr,
			})
		}()

		switch {
		case src.DockerImage != nil:
			img := src.DockerImage
			st := llb.Image(img.Ref, llb.WithMetaResolver(sOpt.Resolver), withConstraints(opts))

			if img.Cmd == nil {
				return st, nil
			}

			eSt, err := generateSourceFromImage(s, name, st, img.Cmd, sOpt, opts...)
			if err != nil {
				return llb.Scratch(), err
			}
			if src.Path != "" {
				pathHandled = true
				return eSt.AddMount(src.Path, llb.Scratch()), nil
			}
			return eSt.Root(), nil
		case src.Git != nil:
			git := src.Git
			// TODO: Pass git secrets
			ref, err := gitutil.ParseGitRef(git.URL)
			if err != nil {
				return llb.Scratch(), fmt.Errorf("could not parse git ref: %w", err)
			}

			var gOpts []llb.GitOption
			if git.KeepGitDir {
				gOpts = append(gOpts, llb.KeepGitDir())
			}
			gOpts = append(gOpts, withConstraints(opts))
			return llb.Git(ref.Remote, ref.Commit, gOpts...), nil
		case src.HTTPS != nil:
			https := src.HTTPS
			opts := []llb.HTTPOption{withConstraints(opts)}
			opts = append(opts, llb.Filename(name))
			return llb.HTTP(https.URL, opts...), nil
		case src.Context != nil:
			srcCtx := src.Context

			st, err := sOpt.GetContext(dockerui.DefaultLocalNameContext, localIncludeExcludeMerge(&src))
			if err != nil {
				return llb.Scratch(), err
			}

			includeExcludeHandled = true
			if src.Path == "" && srcCtx.Name != "" {
				src.Path = srcCtx.Name
			}
			return *st, nil
		case src.Local != nil:
			srcLocal := src.Local

			includeExcludeHandled = true
			if src.Path == "" && srcLocal.Path != "" {
				src.Path = srcLocal.Path
			}

			return llb.Local(dockerui.DefaultLocalNameContext, localIncludeExcludeMerge(&src)), nil
		case src.Build != nil:
			build := src.Build
			var st llb.State

			if build.Context == "" {
				st = llb.Local(dockerui.DefaultLocalNameContext, withConstraints(opts))
			} else {
				ctxState, err := sOpt.GetContext(dockerui.DefaultLocalNameContext, localIncludeExcludeMerge(&src))
				if err != nil {
					return llb.Scratch(), err
				}
				cst := *ctxState

				src2 := src
				if src2.Path == "" && build.Context != "" {
					src2.Path = build.Context
				}

				// This is necessary to have the specified context to be at the
				// root of the state's fs.
				st, _ = handleFilter(&filterOpts{
					state:                 cst,
					source:                src2,
					opts:                  opts,
					forMount:              forMount,
					includeExcludeHandled: true,
					pathHandled:           false,
				})
			}

			return sOpt.Forward(st, build)
		default:
			return llb.Scratch(), fmt.Errorf("No source variant found")
		}
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

func SplitSourceRef(ref string) (string, string, error) {
	scheme, ref, ok := strings.Cut(ref, "://")
	if !ok {
		return "", "", fmt.Errorf("invalid source ref: %s", ref)
	}
	return scheme, ref, nil
}

func WithCreateDestPath() llb.CopyOption {
	return copyOptionFunc(func(i *llb.CopyInfo) {
		i.CreateDestPath = true
	})
}

func SourceIsDir(src Source) (bool, error) {
	switch {
	case src.DockerImage != nil,
		src.Git != nil,
		src.Build != nil,
		src.Context != nil,
		src.Local != nil:
		return true, nil
	case src.HTTPS != nil:
		return false, nil
	default:
		return false, fmt.Errorf("unsupported source type")
	}
}

// Doc returns the details of how the source was created.
// This should be included, where applicable, in build in build specs (such as RPM spec files)
// so that others can reproduce the build.
func (s Source) Doc() (io.Reader, error) {
	b := bytes.NewBuffer(nil)
	switch {
	case s.Context != nil:
		fmt.Fprintln(b, "Generated from a local docker build context and is unreproducible.")
	case s.Local != nil:
		fmt.Fprintln(b, "Generated from a local docker build context and is unreproducible.")
	case s.Build != nil:
		build := s.Build
		fmt.Fprintln(b, "Generated from a docker build:")
		fmt.Fprintln(b, "	Docker Build Target:", s.Build.Target)
		fmt.Fprintln(b, "	Docker Build Ref:", build.Context)

		if len(s.Build.Args) > 0 {
			sorted := SortMapKeys(s.Build.Args)
			fmt.Fprintln(b, "	Build Args:")
			for _, k := range sorted {
				fmt.Fprintf(b, "		%s=%s\n", k, s.Build.Args[k])
			}
		}

		switch {
		case s.Build.Inline != nil:
			fmt.Fprintln(b, "	Dockerfile:")

			scanner := bufio.NewScanner(strings.NewReader(*s.Build.Inline))
			for scanner.Scan() {
				fmt.Fprintf(b, "		%s\n", scanner.Text())
			}
			if scanner.Err() != nil {
				return nil, scanner.Err()
			}
		case s.Build.DockerFile != nil:
			p := "Dockerfile"
			if s.Build.DockerFile != nil {
				p = *s.Build.DockerFile
			}
			fmt.Fprintln(b, "	Dockerfile path in context:", p)
		}
	case s.HTTPS != nil:
		fmt.Fprintln(b, "Generated from a http(s) source:")
		fmt.Fprintln(b, "	URL:", s.HTTPS.URL)
	case s.Git != nil:
		git := s.Git
		ref, err := gitutil.ParseGitRef(git.URL)
		if err != nil {
			return nil, err
		}
		fmt.Fprintln(b, "Generated from a git repository:")
		fmt.Fprintln(b, "	Ref:", ref.Commit)
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
					sub, err := src.Spec.Doc()
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

// `sourceToState` must be a complete map from source name -> llb state for each source in the dalec spec.
// `worker` must be an LLB state with a `patch` binary present.
// PatchSources returns a new map containing the patched LLB state for each source in the source map.
func PatchSources(worker llb.State, spec *Spec, sourceToState map[string]llb.State, opts ...llb.ConstraintsOpt) map[string]llb.State {
	// duplicate map to avoid possibly confusing behavior of mutating caller's map
	states := DuplicateMap(sourceToState)
	pgID := identity.NewID()
	sorted := SortMapKeys(spec.Sources)

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
