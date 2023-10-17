package dalec

import (
	"fmt"
	"strings"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerui"
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

type ForwarderFunc func(llb.State, *BuildSpec) (llb.State, error)

type SourceOpts struct {
	Resolver   llb.ImageMetaResolver
	Forward    ForwarderFunc
	GetContext func(string, ...llb.LocalOption) (*llb.State, error)
}

// must not be called with a nil cmd pointer
func generateSourceFromImage(s *Spec, name string, st llb.State, cmd *CmdSpec, sOpts SourceOpts, opts ...llb.ConstraintsOpt) (llb.ExecState, error) {
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

	for _, src := range cmd.Sources {
		srcSt, err := source2LLBGetter(s, src.Spec, name, true)(sOpts, opts...)
		if err != nil {
			return zero, err
		}
		if src.Copy {
			st = st.File(llb.Copy(srcSt, src.Spec.Path, src.Path, WithCreateDestPath(), WithDirContentsOnly()))
		} else {
			var mountOpt []llb.MountOption
			if src.Spec.Path != "" && len(src.Spec.Includes) == 0 && len(src.Spec.Excludes) == 0 {
				mountOpt = append(mountOpt, llb.SourcePath(src.Spec.Path))
			}
			baseRunOpts = append(baseRunOpts, llb.AddMount(src.Path, srcSt, mountOpt...))
		}
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
