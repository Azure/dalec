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

func withGomod(g *SourceGenerator, srcSt, worker llb.State, opts ...llb.ConstraintsOpt) func(llb.State) llb.State {
	return func(in llb.State) llb.State {
		workDir := "/work/src"
		joinedWorkDir := filepath.Join(workDir, g.Subpath)
		srcMount := llb.AddMount(workDir, srcSt)

		paths := g.Gomod.Paths
		if g.Gomod.Paths == nil {
			paths = []string{"."}
		}

		const proxyPath = "/tmp/dalec/gomod-proxy-cache"

		// Pass in git auth if necessary
		worker = worker.With(g.gitAuth(opts...))

		for _, path := range paths {
			in = worker.Run(
				// First download the go module deps to our persistent cache
				// Then set the GOPROXY to the cache dir so that we can extract just the deps we need
				// This allows us to persist the module cache across builds and avoid downloading
				// the same deps over and over again.
				ShArgs(`set -e; GOMODCACHE="${TMP_GOMODCACHE}" go mod download; GOPROXY="file://${TMP_GOMODCACHE}/cache/download" go mod download`),
				llb.IgnoreCache,
				llb.AddEnv("GOPATH", "/go"),
				llb.AddEnv("TMP_GOMODCACHE", proxyPath),
				llb.Dir(filepath.Join(joinedWorkDir, path)),
				srcMount,
				llb.AddMount(proxyPath, llb.Scratch(), llb.AsPersistentCacheDir(GomodCacheKey, llb.CacheMountShared)),
				WithConstraints(opts...),
			).AddMount(gomodCacheDir, in)
		}
		return in
	}
}

func (g *SourceGenerator) gitAuth(opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(worker llb.State) llb.State {
		if g == nil || g.Gomod == nil {
			return worker
		}

		if len(g.Gomod.Auth) == 0 {
			return worker
		}

		script := new(bytes.Buffer)
		fmt.Fprintln(script, `#!/usr/bin/env sh`)
		secrets := make(map[string]struct{}, len(g.Gomod.Auth))

		for host, auth := range g.Gomod.Auth {
			var headerArg string
			if auth.Header != "" {
				headerArg = fmt.Sprintf(`Authorization: ${%s}`, auth.Header)
				secrets[auth.Header] = struct{}{}
			}

			if auth.Token != "" && headerArg == "" {
				line := fmt.Sprintf(`export tkn="$(echo -n "x-access-token:${%s}" | base64)"`, auth.Token)
				fmt.Fprintln(script, line)

				headerArg = fmt.Sprintf(`Authorization: basic ${tkn}`)
				secrets[auth.Token] = struct{}{}
			}

			if auth.SSH != "" && headerArg == "" {
				panic("unimplemented")
			}

			fmt.Fprintf(script, `git config --global http."https://%s".extraheader "%s"`, host, headerArg)
			script.WriteRune('\n')
		}

		secretsOpt := runOptionFunc(func(ei *llb.ExecInfo) {
			for secret := range secrets {
				SecretToEnv(secret).SetRunOption(ei)
			}
		})

		scriptTxt := string(script.Bytes())
		lines := strings.Split(scriptTxt, "\n")
		_ = lines
		scriptState := llb.Scratch().File(llb.Mkfile("/script.sh", 0o755, script.Bytes()))

		return worker.Run(
			llb.Args([]string{"/tmp/mnt/script.sh"}),
			llb.AddMount("/tmp/mnt", scriptState),
			secretsOpt,
			WithConstraints(opts...),
		).Root()
	}

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
				if gen.Gomod != nil {
					in = in.With(withGomod(gen, patched[key], worker, opts...))
				}
			}
			return in
		})
	}

	return &deps, nil
}
