package dalec

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"
)

const (
	gomodCacheDir = "/go/pkg/mod"
	// GoModCacheKey is the key used to identify the go module cache in the buildkit cache.
	// It is exported only for testing purposes.
	GomodCacheKey = "dalec-gomod-proxy-cache"
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

func withGomod(g *SourceGenerator, srcSt, worker llb.State, credHelper llb.RunOption, opts ...llb.ConstraintsOpt) func(llb.State) llb.State {
	return func(in llb.State) llb.State {
		const (
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
		script := g.gitconfigGeneratorScript(gomodDownloadWrapperBasename)
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
				llb.AddEnv("GOPATH", "/go"),
				credHelper,
				llb.AddEnv("TMP_GOMODCACHE", proxyPath),
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

func (g *SourceGenerator) gitconfigGeneratorScript(scriptPath string) llb.State {
	var script bytes.Buffer

	sortedHosts := SortMapKeys(g.Gomod.Auth)
	if len(sortedHosts) > 0 {
		fmt.Fprintln(&script, `set -eu`)
		script.WriteRune('\n')
	}

	goPrivate := []string{}

	for _, host := range sortedHosts {
		auth := g.Gomod.Auth[host]
		gpHost, _, _ := strings.Cut(host, ":")
		goPrivate = append(goPrivate, gpHost)

		script.WriteRune('\n')
		if sshConfig := auth.SSH; sshConfig != nil {
			username := "git"
			if sshConfig.Username != "" {
				username = sshConfig.Username
			}

			// By default, go will make a request to git for the source of a
			// package, and it will specify the remote url as https://<package
			// name>. Because SSH auth was requested for this host, tell git to
			// use ssh for upstreams with this host name.
			fmt.Fprintf(&script, `git config --global url."ssh://%[1]s@%[2]s/".insteadOf https://%[3]s/`, username, host, gpHost)
			script.WriteRune('\n')
			continue
		}

		var kind string
		switch {
		case auth.Token != "":
			kind = "token"
		case auth.Header != "":
			kind = "header"
		default:
			continue
		}

		fmt.Fprintf(&script, `git config --global credential."https://%[1]s.helper" "/usr/local/bin/frontend credential-helper --kind=%[2]s"`, host, kind)
		script.WriteRune('\n')
	}

	fmt.Fprintf(&script, "go env -w GOPRIVATE=%s", strings.Join(goPrivate, ","))
	script.WriteRune('\n')
	fmt.Fprintln(&script, "cat go.mod && go mod download")
	return llb.Scratch().File(llb.Mkfile(scriptPath, 0o755, script.Bytes()))
}

func (g *SourceGenerator) withGomodSecretsAndSockets() llb.RunOption {
	return runOptionFunc(func(ei *llb.ExecInfo) {
		if g.Gomod == nil {
			return
		}

		const basePath = "/run/secrets"

		for host, auth := range g.Gomod.Auth {
			if auth.Token != "" {
				p := filepath.Join(basePath, host, "token")
				llb.AddSecret(p, llb.SecretID(auth.Token)).SetRunOption(ei)
				continue
			}

			if auth.Header != "" {
				p := filepath.Join(basePath, host, "header")
				llb.AddSecret(p, llb.SecretID(auth.Header)).SetRunOption(ei)
				continue
			}

			if auth.SSH != nil && auth.SSH.ID != "" {
				llb.AddSSHSocket(llb.SSHID(auth.SSH.ID)).SetRunOption(ei)

				llb.AddEnv(
					"GIT_SSH_COMMAND",
					`ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no`,
				).SetRunOption(ei)
			}
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

	credHelperRunOpt, err := sOpt.GitCredHelperOpt()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get git credential helper")
	}

	for _, key := range sorted {
		src := s.Sources[key]

		opts := append(opts, ProgressGroup("Fetch go module dependencies for source: "+key))
		deps = deps.With(func(in llb.State) llb.State {
			for _, gen := range src.Generate {
				in = in.With(withGomod(gen, patched[key], worker, credHelperRunOpt, opts...))
			}
			return in
		})
	}

	return &deps, nil
}
