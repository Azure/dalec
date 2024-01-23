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
	sourcetypes "github.com/moby/buildkit/source/types"
	"github.com/moby/buildkit/util/gitutil"
)

const (
	// Custom source type to generate output from a command.
	sourceTypeContext = "context"
	sourceTypeBuild   = "build"
	sourceTypeSource  = "source"
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
func generateSourceFromImage(s *Spec, name string, st llb.State, cmd *SourceCommand, sOpts SourceOpts, opts ...llb.ConstraintsOpt) (llb.ExecState, error) {
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

func source2LLBGetter(s *Spec, src Source, name string, forMount bool) LLBGetter {
	return func(sOpt SourceOpts, opts ...llb.ConstraintsOpt) (ret llb.State, retErr error) {
		scheme, ref, err := SplitSourceRef(src.Ref)
		if err != nil {
			return llb.Scratch(), err
		}

		var (
			includeExcludeHandled bool
			pathHandled           bool
		)

		defer func() {
			if retErr != nil {
				return
			}
			needsFilter := func() bool {
				if src.Path != "" && !forMount && !pathHandled {
					return true
				}
				if includeExcludeHandled {
					return false
				}
				if len(src.Includes) > 0 || len(src.Excludes) > 0 {
					return true
				}
				return false
			}
			if !needsFilter() {
				return
			}

			srcPath := "/"
			if !pathHandled {
				srcPath = src.Path
			}

			orig := ret
			ret = llb.Scratch().File(
				llb.Copy(
					orig,
					srcPath,
					"/",
					WithIncludes(src.Includes),
					WithExcludes(src.Excludes),
					WithDirContentsOnly(),
				),
				withConstraints(opts),
			)
		}()

		switch scheme {
		case sourcetypes.DockerImageScheme:
			st := llb.Image(ref, llb.WithMetaResolver(sOpt.Resolver), withConstraints(opts))

			if src.Cmd == nil {
				return st, nil
			}

			eSt, err := generateSourceFromImage(s, name, st, src.Cmd, sOpt, opts...)
			if err != nil {
				return llb.Scratch(), err
			}
			if src.Path != "" {
				pathHandled = true
				return eSt.AddMount(src.Path, llb.Scratch()), nil
			}
			return eSt.Root(), nil
		case sourcetypes.GitScheme:
			// TODO: Pass git secrets
			ref, err := gitutil.ParseGitRef(ref)
			if err != nil {
				return llb.Scratch(), fmt.Errorf("could not parse git ref: %w", err)
			}

			var gOpts []llb.GitOption
			if src.KeepGitDir {
				gOpts = append(gOpts, llb.KeepGitDir())
			}
			gOpts = append(gOpts, withConstraints(opts))
			return llb.Git(ref.Remote, ref.Commit, gOpts...), nil
		case sourcetypes.HTTPScheme, sourcetypes.HTTPSScheme:
			ref, err := gitutil.ParseGitRef(src.Ref)
			if err == nil {
				// TODO: Pass git secrets
				var gOpts []llb.GitOption
				if src.KeepGitDir {
					gOpts = append(gOpts, llb.KeepGitDir())
				}
				gOpts = append(gOpts, withConstraints(opts))
				return llb.Git(ref.Remote, ref.Commit, gOpts...), nil
			} else {
				opts := []llb.HTTPOption{withConstraints(opts)}
				opts = append(opts, llb.Filename(name))
				return llb.HTTP(src.Ref, opts...), nil
			}
		case sourceTypeContext:
			st, err := sOpt.GetContext(dockerui.DefaultLocalNameContext, localIncludeExcludeMerge(&src))
			if err != nil {
				return llb.Scratch(), err
			}

			includeExcludeHandled = true
			if src.Path == "" && ref != "" {
				src.Path = ref
			}
			return *st, nil
		case sourceTypeBuild:
			var st llb.State
			if ref == "" {
				st = llb.Local(dockerui.DefaultLocalNameContext, withConstraints(opts))
			} else {
				src2 := Source{
					Ref:        ref,
					Path:       src.Path,
					Includes:   src.Includes,
					Excludes:   src.Excludes,
					KeepGitDir: src.KeepGitDir,
					Cmd:        src.Cmd,
				}
				st, err = source2LLBGetter(s, src2, name, forMount)(sOpt, opts...)
				if err != nil {
					return llb.Scratch(), err
				}
			}

			return sOpt.Forward(st, src.Build)
		case sourceTypeSource:
			src := s.Sources[ref]
			return source2LLBGetter(s, src, name, forMount)(sOpt, opts...)
		default:
			return llb.Scratch(), fmt.Errorf("unsupported source type: %s", scheme)
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
	scheme, _, err := SplitSourceRef(src.Ref)
	if err != nil {
		return false, err
	}
	switch scheme {
	case sourcetypes.DockerImageScheme,
		sourcetypes.GitScheme,
		sourceTypeBuild,
		sourceTypeContext:
		return true, nil
	case sourcetypes.HTTPScheme, sourcetypes.HTTPSScheme:
		if isGitRef(src.Ref) {
			return true, nil
		}
		return false, nil
	default:
		return false, fmt.Errorf("unsupported source type: %s", scheme)
	}
}

func isGitRef(ref string) bool {
	_, err := gitutil.ParseGitRef(ref)
	return err == nil
}

// Doc returns the details of how the source was created.
// This should be included, where applicable, in build in build specs (such as RPM spec files)
// so that others can reproduce the build.
func (s Source) Doc() (io.Reader, error) {
	b := bytes.NewBuffer(nil)
	scheme, ref, err := SplitSourceRef(s.Ref)
	if err != nil {
		return nil, err
	}

	switch scheme {
	case sourceTypeSource:
		fmt.Fprintln(b, "Generated from another source named:", ref)
	case sourceTypeContext:
		fmt.Fprintln(b, "Generated from a local docker build context and is unreproducible.")
	case sourceTypeBuild:
		fmt.Fprintln(b, "Generated from a docker build:")
		fmt.Fprintln(b, "	Docker Build Target:", s.Build.Target)
		fmt.Fprintln(b, "	Docker Build Ref:", ref)

		if len(s.Build.Args) > 0 {
			sorted := SortMapKeys(s.Build.Args)
			fmt.Fprintln(b, "	Build Args:")
			for _, k := range sorted {
				fmt.Fprintf(b, "		%s=%s\n", k, s.Build.Args[k])
			}
		}

		if s.Build.Inline != "" {
			fmt.Fprintln(b, "	Dockerfile:")

			scanner := bufio.NewScanner(strings.NewReader(s.Build.Inline))
			for scanner.Scan() {
				fmt.Fprintf(b, "		%s\n", scanner.Text())
			}
			if scanner.Err() != nil {
				return nil, scanner.Err()
			}
		} else {
			p := "Dockerfile"
			if s.Build.File != "" {
				p = s.Build.File
			}
			fmt.Fprintln(b, "	Dockerfile path in context:", p)
		}
	case sourcetypes.HTTPScheme, sourcetypes.HTTPSScheme:
		ref, err := gitutil.ParseGitRef(ref)
		if err == nil {
			// git ref
			fmt.Fprintln(b, "Generated from a git repository:")
			fmt.Fprintln(b, "	Remote:", scheme+"://"+ref.Remote)
			fmt.Fprintln(b, "	Ref:", ref.Commit)
			if ref.SubDir != "" {
				fmt.Fprintln(b, "	Subdir:", ref.SubDir)
			}
			if s.Path != "" {
				fmt.Fprintln(b, "	Extraced path:", s.Path)
			}
		} else {
			fmt.Fprintln(b, "Generated from a http(s) source:")
			fmt.Fprintln(b, "	URL:", ref)
		}
	case sourcetypes.GitScheme:
		ref, err := gitutil.ParseGitRef(ref)
		if err != nil {
			return nil, err
		}
		fmt.Fprintln(b, "Generated from a git repository:")
		fmt.Fprintln(b, "	Remote:", scheme+"://"+ref.Remote)
		fmt.Fprintln(b, "	Ref:", ref.Commit)
		if s.Path != "" {
			fmt.Fprintln(b, "	Extraced path:", s.Path)
		}
	case sourcetypes.DockerImageScheme:
		if s.Cmd == nil {
			fmt.Fprintln(b, "Generated from a docker image:")
			fmt.Fprintln(b, "	Image:", ref)
			if s.Path != "" {
				fmt.Fprintln(b, "	Extraced path:", s.Path)
			}
		} else {
			fmt.Fprintln(b, "Generated from running a command(s) in a docker image:")
			fmt.Fprintln(b, "	Image:", ref)
			if s.Path != "" {
				fmt.Fprintln(b, "	Extraced path:", s.Path)
			}
			if len(s.Cmd.Env) > 0 {
				fmt.Fprintln(b, "	With the following environment variables set for all commands:")

				sorted := SortMapKeys(s.Cmd.Env)
				for _, k := range sorted {
					fmt.Fprintf(b, "		%s=%s\n", k, s.Cmd.Env[k])
				}
			}
			if s.Cmd.Dir != "" {
				fmt.Fprintln(b, "	Working Directory:", s.Cmd.Dir)
			}
			fmt.Fprintln(b, "	Command(s):")
			for _, step := range s.Cmd.Steps {
				fmt.Fprintf(b, "		%s\n", step.Command)
				if len(step.Env) > 0 {
					fmt.Fprintln(b, "			With the following environment variables set for this command:")
					sorted := SortMapKeys(step.Env)
					for _, k := range sorted {
						fmt.Fprintf(b, "				%s=%s\n", k, step.Env[k])
					}
				}
			}
			if len(s.Cmd.Mounts) > 0 {
				fmt.Fprintln(b, "	With the following items mounted:")
				for _, src := range s.Cmd.Mounts {
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
		fmt.Fprintln(b, "Generated from an unknown source type:", s.Ref)
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
		src := spec.Sources[sourceName]
		sourceState := states[sourceName]

		patches, patchesExist := spec.Patches[sourceName]
		if !patchesExist {
			continue
		}
		pg := llb.ProgressGroup(pgID, "Patch spec source: "+sourceName+" "+src.Ref, false)
		states[sourceName] = patchSource(worker, sourceState, states, patches, pg, withConstraints(opts))
	}

	return states
}
