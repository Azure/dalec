package dalec

import (
	"bytes"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/goccy/go-yaml"
	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"
)

const (
	gomodCacheDir = "/go/pkg/mod"
	// GoModCacheKey is the key used to identify the go module cache in the buildkit cache.
	// It is exported only for testing purposes.
	GomodCacheKey       = "dalec-gomod-proxy-cache"
	gitConfigMountpoint = "/dev/shm/git"
)

func (s *Source) isGomod() bool {
	for _, gen := range s.Generate {
		if gen.Gomod != nil {
			return true
		}
	}
	return false
}

// HasGomods returns true if any of the sources in the spec are a go module.
func (s *Spec) HasGomods() bool {
	for _, src := range s.Sources {
		if src.isGomod() {
			return true
		}
	}
	return false
}

func withGomod(g *SourceGenerator, srcSt, worker, credHelper llb.State, opts ...llb.ConstraintsOpt) func(llb.State) llb.State {
	return func(in llb.State) llb.State {
		const (
			fourKB                       = 4096
			workDir                      = "/work/src"
			scriptMountpoint             = "/tmp/dalec/internal/gomod"
			gomodDownloadWrapperBasename = "go_mod_download.sh"
			authConfigMountPath          = "/tmp/dalec/internal/git_auth_config"
			authConfigBasename           = "authconfig.yml"
			credHelperBaseDir            = "/usr/local/bin"
		)

		joinedWorkDir := filepath.Join(workDir, g.Subpath)
		srcMount := llb.AddMount(workDir, srcSt)

		paths := g.Gomod.Paths
		if g.Gomod.Paths == nil {
			paths = []string{"."}
		}

		const proxyPath = "/tmp/dalec/gomod-proxy-cache"

		// Pass in git auth if necessary
		sort.Strings(paths)
		authConfigPath := filepath.Join(authConfigMountPath, authConfigBasename)
		script := g.gitconfigGeneratorScript(gomodDownloadWrapperBasename, authConfigPath)
		scriptPath := filepath.Join(scriptMountpoint, gomodDownloadWrapperBasename)
		credHelperPath := filepath.Join(credHelperBaseDir, GitCredentialHelperGomod)

		for _, path := range paths {
			in = worker.Run(
				// First download the go module deps to our persistent cache
				// Then set the GOPROXY to the cache dir so that we can extract just the deps we need
				// This allows us to persist the module cache across builds and avoid downloading
				// the same deps over and over again.
				ShArgs(`set -e; GOMODCACHE="${TMP_GOMODCACHE}" `+scriptPath+`; GOPROXY="file://${TMP_GOMODCACHE}/cache/download" `+scriptPath),
				g.withGomodSecretsAndSockets(),
				g.mountGitAuthConfig(authConfigMountPath, authConfigBasename),
				llb.AddMount(scriptMountpoint, script),
				llb.AddEnv("GOPATH", "/go"),
				withCredHelper(credHelper, credHelperPath),
				llb.AddEnv("TMP_GOMODCACHE", proxyPath),
				llb.AddEnv("SSH_AUTH_SOCK", "/sshsock/S.gpg-agent.ssh"),
				llb.AddEnv("GIT_SSH_COMMAND", "ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no"),
				llb.Dir(filepath.Join(joinedWorkDir, path)),
				srcMount,
				llb.AddMount(proxyPath, llb.Scratch(), llb.AsPersistentCacheDir(GomodCacheKey, llb.CacheMountShared)),
				llb.IgnoreCache,
				WithConstraints(opts...),
			).AddMount(gomodCacheDir, in)
		}
		return in
	}
}

func withCredHelper(credHelper llb.State, credHelperPath string) RunOptFunc {
	return func(ei *llb.ExecInfo) {
		llb.AddMount(credHelperPath, credHelper, llb.SourcePath(GitCredentialHelperGomod)).SetRunOption(ei)
	}
}

func (g *SourceGenerator) mountGitAuthConfig(mountPoint, basename string) llb.RunOption {
	return RunOptFunc(func(ei *llb.ExecInfo) {
		if g.Gomod == nil || g.Gomod.Auth == nil {
			return
		}

		b, err := yaml.Marshal(&g.Gomod.Auth)
		if err != nil {
			panic("cannot marshal dalec spec yaml")
		}

		st := llb.Scratch().File(llb.Mkfile("/"+basename, 0o644, b))
		llb.AddMount(mountPoint, st).SetRunOption(ei)
	})
}

func (g *SourceGenerator) gitconfigGeneratorScript(scriptPath, configPath string) llb.State {
	var script bytes.Buffer

	sortedHosts := SortMapKeys(g.Gomod.Auth)
	if len(sortedHosts) > 0 {
		fmt.Fprintln(&script, `set -eu`)
	}

	for _, host := range sortedHosts {
		fmt.Fprintf(&script, `git config --global credential."https://%s".helper "gomod %s"`, host, configPath)
		script.WriteRune('\n')
	}

	fmt.Fprintln(&script, "go mod download")
	return llb.Scratch().File(llb.Mkfile(scriptPath, 0o755, script.Bytes()))
}

func (g *SourceGenerator) withGomodSecretsAndSockets() llb.RunOption {
	return runOptionFunc(func(ei *llb.ExecInfo) {
		if g.Gomod == nil {
			return
		}

		var (
			setenv = func() {
				llb.AddEnv(
					"GIT_SSH_COMMAND",
					`ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no`,
				).SetRunOption(ei)
			}

			noop = func() {}
		)

		secrets := make(map[string]struct{}, len(g.Gomod.Auth))
		for _, auth := range g.Gomod.Auth {
			if auth.Token != "" {
				secrets[auth.Token] = struct{}{}
				continue
			}

			if auth.Header != "" {
				secrets[auth.Header] = struct{}{}
				continue
			}

			if auth.SSH != nil && auth.SSH.ID != "" {
				llb.AddSSHSocket(llb.SSHID(auth.SSH.ID)).SetRunOption(ei)

				setenv()
				setenv = noop
			}
		}

		for secret := range secrets {
			llb.AddSecret("/run/secrets/"+secret, llb.SecretID(secret)).SetRunOption(ei)
		}
	})
}

func (s *Spec) gomodSources() map[string]Source {
	sources := map[string]Source{}
	for name, src := range s.Sources {
		if src.isGomod() {
			sources[name] = src
		}
	}
	return sources
}

// GomodDeps returns an [llb.State] containing all the go module dependencies for the spec
// for any sources that have a gomod generator specified.
// If there are no sources with a gomod generator, this will return a nil state.
func (s *Spec) GomodDeps(sOpt SourceOpts, worker llb.State, opts ...llb.ConstraintsOpt) (*llb.State, error) {
	sources := s.gomodSources()
	if len(sources) == 0 {
		return nil, nil
	}

	deps := llb.Scratch()

	// Get the patched sources for the go modules
	// This is needed in case a patch includes changes to go.mod or go.sum
	patched, err := s.getPatchedSources(sOpt, worker, func(name string) bool {
		_, ok := sources[name]
		return ok
	}, opts...)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get patched sources")
	}

	sorted := SortMapKeys(patched)

	credHelper, err := sOpt.GitCredentialHelpers[GitCredentialHelperGomod]()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get git credential helper")
	}

	for _, key := range sorted {
		src := s.Sources[key]

		opts := append(opts, ProgressGroup("Fetch go module dependencies for source: "+key))
		deps = deps.With(func(in llb.State) llb.State {
			for _, gen := range src.Generate {
				in = in.With(withGomod(gen, patched[key], worker, credHelper, opts...))
			}
			return in
		})
	}

	return &deps, nil
}
