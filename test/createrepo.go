package test

import (
	"github.com/Azure/dalec"
	"github.com/Azure/dalec/targets/linux/rpm/distro"
	"github.com/moby/buildkit/client/llb"
)

func createYumRepo(installer *distro.Config) func(rpms llb.State, opts ...llb.StateOption) llb.StateOption {
	return func(rpms llb.State, opts ...llb.StateOption) llb.StateOption {
		return func(in llb.State) llb.State {
			localRepo := []byte(`
[Local]
name=Local Repository
baseurl=file:///opt/repo
gpgcheck=0
priority=0
enabled=1
`)

			pg := dalec.ProgressGroup("Install local repo for test")
			withRepos := in.
				Run(installer.Install([]string{"createrepo"}), pg).
				File(llb.Mkdir("/opt/repo/RPMS", 0o755, llb.WithParents(true)), pg).
				File(llb.Mkdir("/opt/repo/SRPMS", 0o755), pg).
				File(llb.Mkfile("/etc/yum.repos.d/local.repo", 0o644, localRepo), pg).
				Run(
					llb.AddMount("/tmp/st", rpms, llb.Readonly),
					dalec.ShArgs("cp /tmp/st/RPMS/$(uname -m)/* /opt/repo/RPMS/ && cp /tmp/st/SRPMS/* /opt/repo/SRPMS"),
					pg,
				).
				Run(dalec.ShArgs("createrepo --compatibility /opt/repo"), pg).
				Root()

			for _, opt := range opts {
				withRepos = withRepos.With(opt)
			}

			return withRepos
		}
	}
}
