package frontend

import (
	"fmt"
	"strings"

	"github.com/moby/buildkit/client/llb"
	sourcetypes "github.com/moby/buildkit/source/types"
	"github.com/moby/buildkit/util/gitutil"
)

func WithInternalName(name string) llb.ConstraintsOpt {
	return llb.WithCustomNamef("[internal] %s", name)
}

func WithInternalNamef(format string, a ...interface{}) llb.ConstraintsOpt {
	return llb.WithCustomNamef("[internal] "+format, a...)
}

func Source2LLB(src Source) (llb.State, error) {
	scheme, ref, err := SplitSourceRef(src.Ref)
	if err != nil {
		return llb.Scratch(), err
	}

	var st llb.State
	switch scheme {
	case sourcetypes.DockerImageScheme:
		st = llb.Image(ref, llb.WithCustomName("Fetch docker image source: "+ref))
	case sourcetypes.GitScheme:
		// TODO: Pass git secrets
		ref, err := gitutil.ParseGitRef(ref)
		if err != nil {
			return llb.Scratch(), fmt.Errorf("could not parse git ref: %w", err)
		}
		var opts []llb.GitOption
		if src.KeepGitDir {
			opts = append(opts, llb.KeepGitDir())
		}
		opts = append(opts, llb.WithCustomName("Fetch git source: "+ref.Remote+"@"+ref.Commit))
		st = llb.Git(ref.Remote, ref.Commit, opts...)
	case sourcetypes.HTTPScheme, sourcetypes.HTTPSScheme:
		ref, err := gitutil.ParseGitRef(src.Ref)
		if err == nil {
			// TODO: Pass git secrets
			var opts []llb.GitOption
			if src.KeepGitDir {
				opts = append(opts, llb.KeepGitDir())
			}
			opts = append(opts, llb.WithCustomName("Fetch git source: "+ref.Remote+"@"+ref.Commit))
			st = llb.Git(ref.Remote, ref.Commit, opts...)
		} else {
			st = llb.HTTP(src.Ref, llb.WithCustomName("Fetch http source: "+src.Ref))
		}
	case sourcetypes.LocalScheme:
		st = llb.Local(ref, llb.WithCustomName("Fetch local source: "+ref))
	}

	if src.Path != "" || len(src.Includes) > 0 || len(src.Excludes) > 0 {
		st = llb.Scratch().File(
			llb.Copy(
				st,
				src.Path,
				"/",
				withIncludes(src.Includes),
				withExcludes(src.Excludes),
			),
			WithInternalNamef("Get source subpath and filter includes/excludes: %s @ %s", src.Ref, src.Path),
		)
	}
	return st, nil
}

type copyOptionFunc func(*llb.CopyInfo)

func (f copyOptionFunc) SetCopyOption(i *llb.CopyInfo) {
	f(i)
}

func withIncludes(patterns []string) llb.CopyOption {
	return copyOptionFunc(func(i *llb.CopyInfo) {
		i.IncludePatterns = patterns
	})
}

func withExcludes(patterns []string) llb.CopyOption {
	return copyOptionFunc(func(i *llb.CopyInfo) {
		i.ExcludePatterns = patterns
	})
}

func SplitSourceRef(ref string) (string, string, error) {
	scheme, ref, ok := strings.Cut(ref, "://")
	if !ok {
		return "", "", fmt.Errorf("invalid source ref: %s", ref)
	}
	return scheme, ref, nil
}

func SourceIsDir(src Source) (bool, error) {
	scheme, _, err := SplitSourceRef(src.Ref)
	if err != nil {
		return false, err
	}
	switch scheme {
	case sourcetypes.DockerImageScheme,
		sourcetypes.GitScheme,
		sourcetypes.LocalScheme:
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
