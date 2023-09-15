package frontend

import "github.com/moby/buildkit/client/llb"

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
