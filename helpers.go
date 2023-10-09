package dalec

import (
	"encoding/json"
	"sort"

	"github.com/moby/buildkit/client/llb"
)

type copyOptionFunc func(*llb.CopyInfo)

func (f copyOptionFunc) SetCopyOption(i *llb.CopyInfo) {
	f(i)
}

func WithIncludes(patterns []string) llb.CopyOption {
	return copyOptionFunc(func(i *llb.CopyInfo) {
		i.IncludePatterns = patterns
	})
}

func WithExcludes(patterns []string) llb.CopyOption {
	return copyOptionFunc(func(i *llb.CopyInfo) {
		i.ExcludePatterns = patterns
	})
}

func WithDirContentsOnly() llb.CopyOption {
	return copyOptionFunc(func(i *llb.CopyInfo) {
		i.CopyDirContentsOnly = true
	})
}

type constraintsOptFunc func(*llb.Constraints)

func (f constraintsOptFunc) SetConstraintsOption(c *llb.Constraints) {
	f(c)
}

func (f constraintsOptFunc) SetRunOption(ei *llb.ExecInfo) {
	f(&ei.Constraints)
}

func (f constraintsOptFunc) SetLocalOption(li *llb.LocalInfo) {
	f(&li.Constraints)
}

func (f constraintsOptFunc) SetOCILayoutOption(oi *llb.OCILayoutInfo) {
	f(&oi.Constraints)
}

func (f constraintsOptFunc) SetHTTPOption(hi *llb.HTTPInfo) {
	f(&hi.Constraints)
}

func (f constraintsOptFunc) SetImageOption(ii *llb.ImageInfo) {
	f(&ii.Constraints)
}

func (f constraintsOptFunc) SetGitOption(gi *llb.GitInfo) {
	f(&gi.Constraints)
}

func WithConstraints(ls ...llb.ConstraintsOpt) llb.ConstraintsOpt {
	return constraintsOptFunc(func(c *llb.Constraints) {
		for _, opt := range ls {
			opt.SetConstraintsOption(c)
		}
	})
}

func withConstraints(opts []llb.ConstraintsOpt) llb.ConstraintsOpt {
	return WithConstraints(opts...)
}

// SortMapKeys is a convenience generic function to sort the keys of a map[string]T
func SortMapKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// MergeAtPath merges the given states into the given destination path in the given input state.
func MergeAtPath(input llb.State, states []llb.State, dest string) llb.State {
	diffs := make([]llb.State, 0, len(states)+1)
	diffs = append(diffs, input)

	for _, src := range states {
		st := src
		if dest != "" && dest != "/" {
			st = llb.Scratch().
				File(llb.Copy(src, "/", dest, WithCreateDestPath()))
		}
		diffs = append(diffs, llb.Diff(input, st))
	}
	return llb.Merge(diffs)
}

type localOptionFunc func(*llb.LocalInfo)

func (f localOptionFunc) SetLocalOption(li *llb.LocalInfo) {
	f(li)
}

func localIncludeExcludeMerge(src *Source) localOptionFunc {
	return func(li *llb.LocalInfo) {
		if len(src.Excludes) > 0 {
			excludes := src.Excludes
			if li.ExcludePatterns != "" {
				var ls []string
				if err := json.Unmarshal([]byte(li.ExcludePatterns), &ls); err != nil {
					panic(err)
				}
				excludes = append(excludes, ls...)
			}
			llb.ExcludePatterns(excludes).SetLocalOption(li)
		}

		if len(src.Includes) > 0 {
			includes := src.Includes
			if li.IncludePatterns != "" {
				var ls []string
				if err := json.Unmarshal([]byte(li.IncludePatterns), &ls); err != nil {
					panic(err)
				}
				includes = append(includes, ls...)
			}
			llb.IncludePatterns(includes).SetLocalOption(li)
		}
	}
}
