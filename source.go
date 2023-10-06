package dalec

import (
	"fmt"
	"path/filepath"
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

type LLBGetter func(forwarder ForwarderFunc, opts ...llb.ConstraintsOpt) (llb.State, error)

type ForwarderFunc func(llb.State, *BuildSpec) (llb.State, error)

func generateSourceFromImage(s *Spec, st llb.State, cmd *CmdSpec, resolver llb.ImageMetaResolver, forward ForwarderFunc, opts ...llb.ConstraintsOpt) (llb.State, error) {
	if cmd == nil {
		return st, nil
	}

	if len(cmd.Steps) == 0 {
		return llb.Scratch(), fmt.Errorf("no steps defined for image source")
	}

	for k, v := range cmd.Env {
		st = st.AddEnv(k, v)
	}
	if cmd.Dir != "" {
		st = st.Dir(cmd.Dir)
	}

	var mounts []llb.RunOption
	for p, cfg := range cmd.CacheDirs {
		id := cfg.Key
		if id == "" {
			id = p
		}

		m, err := sharingMode(cfg.Mode)
		if err != nil {
			return llb.Scratch(), err
		}
		mounts = append(mounts, llb.AddMount(p, llb.Scratch(), llb.AsPersistentCacheDir(id, m)))
	}

	for _, src := range cmd.Sources {
		srcSt, err := source2LLBGetter(s, src.Spec, resolver, true)(forward, opts...)
		if err != nil {
			return llb.Scratch(), err
		}
		if src.Copy {
			st = st.File(llb.Copy(srcSt, src.Spec.Path, src.Path, WithCreateDestPath(), WithDirContentsOnly()))
		} else {
			var mountOpt []llb.MountOption
			if src.Spec.Path != "" && len(src.Spec.Includes) == 0 && len(src.Spec.Excludes) == 0 {
				mountOpt = append(mountOpt, llb.SourcePath(src.Spec.Path))
			}
			mounts = append(mounts, llb.AddMount(src.Path, srcSt, mountOpt...))
		}
	}

	for _, step := range cmd.Steps {
		rOpts := []llb.RunOption{llb.Args([]string{
			"/bin/sh", "-c", step.Command,
		})}

		rOpts = append(rOpts, mounts...)

		for k, v := range step.Env {
			rOpts = append(rOpts, llb.AddEnv(k, v))
		}

		rOpts = append(rOpts, withConstraints(opts))
		cmdSt := st.Run(rOpts...)
		st = cmdSt.State
	}
	return st, nil
}

func Source2LLBGetter(s *Spec, src Source, mr llb.ImageMetaResolver) LLBGetter {
	return source2LLBGetter(s, src, mr, false)
}

func source2LLBGetter(s *Spec, src Source, mr llb.ImageMetaResolver, forMount bool) LLBGetter {
	return func(forward ForwarderFunc, opts ...llb.ConstraintsOpt) (ret llb.State, retErr error) {
		scheme, ref, err := SplitSourceRef(src.Ref)
		if err != nil {
			return llb.Scratch(), err
		}

		var includeExcludeHandled bool

		defer func() {
			if retErr != nil {
				return
			}
			needsFilter := func() bool {
				if src.Path != "" && !forMount {
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
			orig := ret
			ret = llb.Scratch().File(
				llb.Copy(
					orig,
					src.Path,
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
			return generateSourceFromImage(s, llb.Image(ref, llb.WithMetaResolver(mr)), src.Cmd, mr, forward, opts...)
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
				return llb.HTTP(src.Ref, withConstraints(opts)), nil
			}
		case sourceTypeContext:
			lOpts := []llb.LocalOption{withConstraints(opts)}
			if len(src.Includes) > 0 {
				lOpts = append(lOpts, llb.IncludePatterns(src.Includes))
			}
			if len(src.Excludes) > 0 {
				lOpts = append(lOpts, llb.ExcludePatterns(src.Excludes))
			}
			includeExcludeHandled = true
			if src.Path == "" && ref != "" {
				src.Path = ref
			}
			return llb.Local(filepath.Join(dockerui.DefaultLocalNameContext), lOpts...), nil
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
				st, err = source2LLBGetter(s, src2, mr, forMount)(forward, opts...)
				if err != nil {
					return llb.Scratch(), err
				}
			}

			return forward(st, src.Build)
		case sourceTypeSource:
			src := s.Sources[ref]
			return source2LLBGetter(s, src, mr, forMount)(forward, opts...)
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
