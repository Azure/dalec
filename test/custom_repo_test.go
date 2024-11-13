package test

import (
	"context"
	"testing"

	"github.com/Azure/dalec"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

func testCustomRepo(ctx context.Context, t *testing.T, workerCfg workerConfig, targetCfg targetConfig) {
	// provide a unique suffix per test otherwise, depending on the test case,
	// you can end up with a false positive result due to apt package caching.
	// e.g. there may not be a public key for the repo under test, but if the
	// package is already in the package cache (due to other tests that injected
	// a public key) then apt may use that package anyway.
	getDepSpec := func(suffix string) *dalec.Spec {
		return &dalec.Spec{
			Name:        "dalec-test-package" + suffix,
			Version:     "0.0.1",
			Revision:    "1",
			Description: "A basic package for various testing uses",
			License:     "MIT",
			Sources: map[string]dalec.Source{
				"version.txt": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents: "version: " + "0.0.1",
						},
					},
				},
			},

			Artifacts: dalec.Artifacts{
				Docs: map[string]dalec.ArtifactConfig{
					"version.txt": {},
				},
			},
		}
	}

	getSpec := func(dep *dalec.Spec, keyConfig map[string]dalec.Source) *dalec.Spec {
		spec := &dalec.Spec{
			Name:        "dalec-test-custom-repo",
			Version:     "0.0.1",
			Revision:    "1",
			Description: "Testing allowing a custom repo to be provided",
			License:     "MIT",
			Dependencies: &dalec.PackageDependencies{
				Build: map[string]dalec.PackageConstraints{
					dep.Name: {},
				},
				Runtime: map[string]dalec.PackageConstraints{
					dep.Name: {},
				},

				Test: []string{
					dep.Name,
					"bash",
					"coreutils",
				},

				ExtraRepos: []dalec.PackageRepositoryConfig{
					{
						Config: workerCfg.TestRepoConfig,
						Data: []dalec.SourceMount{
							{
								Dest: "/opt/repo",
								Spec: dalec.Source{
									Context: &dalec.SourceContext{
										Name: "test-repo",
									},
								},
							},
						},
						Keys: keyConfig,
						Envs: []string{"build", "install", "test"},
					},
				},
			},

			Build: dalec.ArtifactBuild{
				Steps: []dalec.BuildStep{
					{
						Command: `set -x; [ "$(cat /usr/share/doc/` + dep.Name + `/version.txt)" = "version: 0.0.1" ]`,
					},
				},
			},

			Tests: []*dalec.TestSpec{
				{
					Name: "Check test dependency installed from custom repo",
					// Dummy command here to force test steps to run and install test stage dependency
					// from custom repo
					Steps: []dalec.TestStep{
						{
							Command: "ls -lrt",
						},
					},
				},
			},
		}

		if workerCfg.Platform != nil && workerCfg.Platform.OS == "windows" {
			spec.Dependencies.Runtime = nil
			spec.Dependencies.Test = nil
			spec.Tests = nil
		}
		return spec
	}

	getRepoState := func(ctx context.Context, t *testing.T, client gwclient.Client, w llb.State, key llb.State, depSpec *dalec.Spec) llb.State {
		sr := newSolveRequest(withSpec(ctx, t, depSpec), withBuildTarget(targetCfg.Package))
		pkg := reqToState(ctx, client, sr, t)

		// create a repo using our existing worker
		workerWithRepo := w.With(workerCfg.CreateRepo(pkg, workerCfg.SignRepo(key)))

		// copy out just the contents of the repo
		return llb.Scratch().File(llb.Copy(workerWithRepo, "/opt/repo", "/", &llb.CopyInfo{CopyDirContentsOnly: true}))
	}

	t.Run("no public key", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)

		testNoPublicKey := func(ctx context.Context, gwc gwclient.Client) {
			sr := newSolveRequest(withBuildTarget(targetCfg.Worker), withSpec(ctx, t, nil))
			w := reqToState(ctx, gwc, sr, t)

			// generate a gpg public/private key pair
			gpgKey := generateGPGKey(w)

			depSpec := getDepSpec("no-public-key")
			repoState := getRepoState(ctx, t, gwc, w, gpgKey, depSpec)

			sr = newSolveRequest(
				withSpec(ctx, t, getSpec(depSpec, nil)),
				withBuildContext(ctx, t, "test-repo", repoState),
				withBuildTarget(targetCfg.Container),
				withPlatformPtr(workerCfg.Platform),
			)

			_, err := gwc.Solve(ctx, sr)
			if err == nil {
				t.Fatal("expected solve to fail")
			}
		}

		testEnv.RunTest(ctx, t, testNoPublicKey)
	})

	t.Run("with public key", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)

		testWithPublicKey := func(ctx context.Context, gwc gwclient.Client) {
			sr := newSolveRequest(withBuildTarget(targetCfg.Worker), withSpec(ctx, t, nil))
			w := reqToState(ctx, gwc, sr, t)

			// generate a gpg key to sign the repo
			// under /public.key
			gpgKey := generateGPGKey(w)
			depSpec := getDepSpec("with-public-key")
			repoState := getRepoState(ctx, t, gwc, w, gpgKey, depSpec)

			keyName := "public.asc"
			spec := getSpec(depSpec, map[string]dalec.Source{
				// in the dalec spec, the public key will be passed in via build context
				keyName: {
					Context: &dalec.SourceContext{
						Name: "repo-public-key",
					},
					Path: "public.asc",
				},
			})

			sr = newSolveRequest(
				withSpec(ctx, t, spec),
				withBuildContext(ctx, t, "test-repo", repoState),
				withBuildContext(ctx, t, "repo-public-key", gpgKey),
				withBuildTarget(targetCfg.Container),
				withPlatformPtr(workerCfg.Platform),
			)

			res := solveT(ctx, t, gwc, sr)
			_, err := res.SingleRef()
			if err != nil {
				t.Fatal(err)
			}
		}

		testEnv.RunTest(ctx, t, testWithPublicKey)
	})
}

func generateGPGKey(worker llb.State) llb.State {
	pg := dalec.ProgressGroup("Generate GPG Key for Testing")

	st := worker.
		Run(dalec.ShArgs(`gpg --batch --gen-key <<EOF
Key-Type: RSA
Key-Length: 2048
Subkey-Type: RSA
Subkey-Length: 2048
Name-Real: Test User
Name-Comment: Test Key
Name-Email: test@example.com
Expire-Date: 0
%no-protection
%commit
EOF
		`), pg).
		Run(dalec.ShArgs("gpg --export --armor test@example.com > /tmp/gpg/public.asc; gpg --export-secret-keys --armor test@example.com > /tmp/gpg/private.key"), pg).
		AddMount("/tmp/gpg", llb.Scratch())

	return st
}
