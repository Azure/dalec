package dalec

import (
	"bytes"
	"fmt"
	"path/filepath"
	"sort"

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

func withGomod(g *SourceGenerator, srcSt, worker llb.State, opts ...llb.ConstraintsOpt) func(llb.State) llb.State {
	return func(in llb.State) llb.State {
		const (
			fourKB                       = 4096
			workDir                      = "/work/src"
			scriptMountpoint             = "/tmp/dalec/internal/gomod"
			gomodDownloadWrapperBasename = "go_mod_download.sh"
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
		script := g.gitconfigGeneratorScript()
		scriptPath := filepath.Join(scriptMountpoint, gomodDownloadWrapperBasename)

		for _, path := range paths {
			in = worker.Run(
				// First download the go module deps to our persistent cache
				// Then set the GOPROXY to the cache dir so that we can extract just the deps we need
				// This allows us to persist the module cache across builds and avoid downloading
				// the same deps over and over again.
				ShArgs(`set -e; GOMODCACHE="${TMP_GOMODCACHE}" `+scriptPath+`; GOPROXY="file://${TMP_GOMODCACHE}/cache/download" `+scriptPath),
				g.withGomodSecretsAndSockets(),
				llb.AddMount(scriptMountpoint, script),
				llb.AddMount(gitConfigMountpoint, llb.Scratch(), llb.Tmpfs(llb.TmpfsSize(fourKB))), // to house the gitconfig, which has secrets
				llb.IgnoreCache,
				llb.AddEnv("GOPATH", "/go"),
				llb.AddEnv("TMP_GOMODCACHE", proxyPath),
				llb.AddEnv("SSH_AUTH_SOCK", "/sshsock/S.gpg-agent.ssh"),
				llb.AddEnv("GIT_SSH_COMMAND", "ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no"),
				llb.Dir(filepath.Join(joinedWorkDir, path)),
				srcMount,
				llb.AddMount(proxyPath, llb.Scratch(), llb.AsPersistentCacheDir(GomodCacheKey, llb.CacheMountShared)),
				WithConstraints(opts...),
			).AddMount(gomodCacheDir, in)
		}
		return in
	}
}

func (g *SourceGenerator) gitconfigGeneratorScript() llb.State {
	var (
		script bytes.Buffer
		noop   = func() {}

		createPreamble = func() {
			fmt.Fprintln(&script, `set -eu`)
			fmt.Fprintf(&script, `ln -sf %s/.gitconfig "${HOME}/.gitconfig"`, gitConfigMountpoint)
			script.WriteRune('\n')
		}
	)

	fmt.Fprintln(&script, `#!/usr/bin/env sh`)

	for host, auth := range g.Gomod.Auth {
		// Only do this the first time through the loop
		createPreamble()
		createPreamble = noop

		var headerArg string
		if auth.Header != "" {
			headerArg = fmt.Sprintf(`Authorization: ${%s}`, auth.Header)
		}

		if auth.Token != "" && headerArg == "" {
			line := fmt.Sprintf(`tkn="$(echo -n "x-access-token:${%s}" | base64)"`, auth.Token)
			fmt.Fprintln(&script, line)

			headerArg = `Authorization: basic ${tkn}`
		}

		if headerArg != "" {
			fmt.Fprintf(&script, `git config --global http."https://%s".extraheader "%s"`, host, headerArg)
			script.WriteRune('\n')
			continue
		}

		username := "git"
		if auth.SSH != nil {
			if auth.SSH.Username != "" {
				username = auth.SSH.Username
			}

			fmt.Fprintf(&script, `git config --global "url.ssh://%[1]s@%[2]s/.insteadOf" https://%[2]s/`, username, host)
			script.WriteRune('\n')
		}
	}

	fmt.Fprintln(&script, "go mod download")
	return llb.Scratch().File(llb.Mkfile(scriptRelativePath, 0o755, script.Bytes()))
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
			secretToEnv(secret).SetRunOption(ei)
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

	for _, key := range sorted {
		src := s.Sources[key]

		opts := append(opts, ProgressGroup("Fetch go module dependencies for source: "+key))
		deps = deps.With(func(in llb.State) llb.State {
			for _, gen := range src.Generate {
				in = in.With(withGomod(gen, patched[key], worker, opts...))
			}
			return in
		})
	}

	return &deps, nil
}
