package distro

import (
	"fmt"

	"github.com/Azure/dalec"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

func (c *Config) BuildContainer(client gwclient.Client, worker llb.State, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, debSt llb.State, opts ...llb.ConstraintsOpt) (llb.State, error) {
	bi, err := spec.GetSingleBase(targetKey)
	if err != nil {
		return llb.Scratch(), err
	}

	var baseImg llb.State
	if bi != nil {
		img, err := bi.ToState(sOpt, opts...)
		if err != nil {
			return llb.Scratch(), err
		}
		baseImg = img
	} else {
		if c.DefaultOutputImage == "" {
			return llb.Scratch(), fmt.Errorf("no output image ref specified, cannot build from scratch")
		}
		baseImg = llb.Image(c.DefaultOutputImage, llb.WithMetaResolver(sOpt.Resolver), dalec.WithConstraints(opts...))
	}

	opts = append(opts, dalec.ProgressGroup("Build Container Image"))

	repos := dalec.GetExtraRepos(c.ExtraRepos, "install")
	repos = append(repos, spec.GetInstallRepos(targetKey)...)

	withRepos, err := c.RepoMounts(repos, sOpt, opts...)
	if err != nil {
		return llb.Scratch(), err
	}

	debug := llb.Scratch().File(llb.Mkfile("debug", 0o644, []byte(`debug=2`)), opts...)
	opts = append(opts, dalec.ProgressGroup("Install spec package"))

	return baseImg.Run(
		dalec.WithConstraints(opts...),
		withRepos,
		llb.AddEnv("DEBIAN_FRONTEND", "noninteractive"),
		dalec.WithMountedAptCache(c.AptCachePrefix),
		// This file makes dpkg give more verbose output which can be useful when things go awry.
		llb.AddMount("/etc/dpkg/dpkg.cfg.d/99-dalec-debug", debug, llb.SourcePath("debug"), llb.Readonly),
		dalec.RunOptFunc(func(cfg *llb.ExecInfo) {
			tmp := llb.Scratch().File(llb.Mkfile("tmp", 0o644, nil), opts...)
			// Warning: HACK here
			// The base ubuntu image has this `excludes` config file which prevents
			// installation of a lot of things, including doc files.
			// This is mounting over that file with an empty file so that our test suite
			// passes (as it is looking at these files).
			llb.AddMount("/etc/dpkg/dpkg.cfg.d/excludes", tmp, llb.SourcePath("tmp")).SetRunOption(cfg)
		}),
		InstallLocalPkg(debSt, opts...),
	).Root().
		With(c.createSymlinks(worker, spec, targetKey, opts...)), nil
}

func (c *Config) createSymlinks(worker llb.State, spec *dalec.Spec, targetKey string, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		post := spec.GetImagePost(targetKey)
		if post == nil {
			return in
		}

		if len(post.Symlinks) == 0 {
			return in
		}

		const workPath = "/tmp/rootfs"
		return worker.
			Run(dalec.WithConstraints(opts...), dalec.InstallPostSymlinks(post, workPath)).
			AddMount(workPath, in)
	}
}
