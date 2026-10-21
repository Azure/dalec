package test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/project-dalec/dalec"
	"gotest.tools/v3/assert"
)

func testNewestRuntimeDepWithinBounds(ctx context.Context, t *testing.T, cfg testLinuxConfig) {
	const pkgName = "dalec-test-package-bounded"
	const marker = "dalec-runtime-consumer"

	for _, tc := range []struct {
		name          string
		newerConsumer bool
	}{
		{name: "simple repository"},
		{name: "the local consumer is retained despite a newer repository version without runtime dependencies", newerConsumer: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
				worker, pkgs, repoPath := versionedDependencyRepo(ctx, t, client, cfg, pkgName)
				repo := versionedDependencyRepoConfig(cfg, repoPath)
				repo.Envs = []string{"install"}

				spec := &dalec.Spec{
					Name:        "dalec-test-bounded-runtime-deps",
					Version:     "0.0.1",
					Revision:    "1",
					Description: "Select the newest runtime dependency within version bounds",
					License:     "MIT",
					Dependencies: &dalec.PackageDependencies{
						Runtime: map[string]dalec.PackageConstraints{
							pkgName: {Version: []string{">= 1.1.1", "< 1.2.0"}},
						},
						ExtraRepos: []dalec.PackageRepositoryConfig{repo},
					},
					Build: dalec.ArtifactBuild{
						Steps: []dalec.BuildStep{{
							Command: "test ! -e /usr/bin/dalec-test-version && echo runtime-consumer-0.0.1 > " + marker,
						}},
					},
					Artifacts: dalec.Artifacts{
						Binaries: map[string]dalec.ArtifactConfig{marker: {}},
					},
				}

				if tc.newerConsumer {
					// Replacing the locally built consumer with this newer package
					// would let the solver evade the local runtime constraints.
					repoConsumer := &dalec.Spec{
						Name:        spec.Name,
						Version:     "1.0.0",
						Revision:    "1",
						Description: "A newer consumer without runtime dependencies",
						License:     "MIT",
						Build: dalec.ArtifactBuild{
							Steps: []dalec.BuildStep{{
								Command: "echo repository-consumer-1.0.0 > " + marker,
							}},
						},
						Artifacts: dalec.Artifacts{
							Binaries: map[string]dalec.ArtifactConfig{marker: {}},
						},
					}

					pkg := reqToState(ctx, client, newSolveRequest(
						withSpec(ctx, t, repoConsumer),
						withBuildTarget(cfg.Target.Package),
					), t)

					repoPath = filepath.Join("/opt/repo", createRepoSuffix())
					worker = worker.With(cfg.Worker.CreateRepo(llb.Merge(append(pkgs, pkg)), repoPath))
					repo = versionedDependencyRepoConfig(cfg, repoPath)
					repo.Envs = []string{"install"}
					spec.Dependencies.ExtraRepos = []dalec.PackageRepositoryConfig{repo}
				}

				req := newSolveRequest(
					withSpec(ctx, t, spec),
					withBuildContext(ctx, t, cfg.Worker.ContextName, worker),
					withBuildContext(ctx, t, "bounded-fixture-worker", worker),
					withBuildTarget(cfg.Target.Container),
					withPlatformPtr(cfg.Worker.Platform),
				)
				res := solveT(ctx, t, client, req)

				// Read the exported root, not the worker used to install into it.
				assert.Equal(t, string(readFile(ctx, t, "/usr/bin/"+marker, res)), "runtime-consumer-0.0.1\n")
				script := string(readFile(ctx, t, "/usr/bin/dalec-test-version", res))
				// RPM's brp-mangle-shebangs can replace the env-based interpreter.
				assert.Assert(t, script == "#!/usr/bin/env bash\necho version: 1.1.9" ||
					script == "#!/usr/bin/bash\necho version: 1.1.9", "unexpected installed version script: %q", script)
			})
		})
	}
}

func testBuildDepPreservesInstalledPackages(ctx context.Context, t *testing.T, cfg testLinuxConfig) {
	const pkgName = "dalec-test-package-bounded"
	const keeperName = "dalec-test-keeper"
	const keeperMarker = "dalec-keeper-marker"

	testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
		pkgs := versionedDependencyPackages(ctx, t, client, cfg, pkgName, func(spec *dalec.Spec) {
			if spec.Version == "1.1.9" {
				spec.Conflicts = dalec.PackageDependencyList{keeperName: {}}
			}
		})
		keeper := &dalec.Spec{
			Name:        keeperName,
			Version:     "1",
			Revision:    "1",
			Description: "An installed package conflicting with the newest allowed dependency",
			License:     "MIT",
			Sources: map[string]dalec.Source{
				keeperMarker: {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{Contents: "keeper\n"},
					},
				},
			},
			Artifacts: dalec.Artifacts{
				Binaries: map[string]dalec.ArtifactConfig{keeperMarker: {}},
			},
		}
		keeperPkg := reqToState(ctx, client, newSolveRequest(
			withSpec(ctx, t, keeper),
			withBuildTarget(cfg.Target.Package),
		), t)
		pkgs = append(pkgs, keeperPkg)

		worker := reqToState(ctx, client, newSolveRequest(
			withBuildTarget(cfg.Target.Worker),
			withSpec(ctx, t, nil),
		), t)
		// Keep the installed package available in the repo, not obsolete.
		worker = worker.With(cfg.Worker.CreateRepo(llb.Merge(pkgs), filepath.Join("/opt/repo", createRepoSuffix())))
		worker = worker.Run(dalec.ShArgs("apt-get update && apt-get install -y " + keeperName + " && apt-mark manual " + keeperName)).Root()

		spec := &dalec.Spec{
			Name:        "dalec-test-keeper-build-deps",
			Version:     "0.0.1",
			Revision:    "1",
			Description: "The newest compatible dependency version does not uninstall conflicting packages",
			License:     "MIT",
			Dependencies: &dalec.PackageDependencies{
				Build: dalec.PackageDependencyList{
					pkgName: {Version: []string{">= 1.1.1", "< 1.2.0"}},
				},
			},
			Build: dalec.ArtifactBuild{
				Steps: []dalec.BuildStep{
					{Command: `set -x; test "$(dalec-test-version)" = "version: 1.1.6"`},
					{Command: "test -f /usr/bin/" + keeperMarker},
				},
			},
		}

		res := solveT(ctx, t, client, newSolveRequest(
			withSpec(ctx, t, spec),
			withBuildContext(ctx, t, cfg.Worker.ContextName, worker),
			withBuildTarget(cfg.Target.Container),
			withPlatformPtr(cfg.Worker.Platform),
		))
		_, err := res.SingleRef()
		assert.NilError(t, err)
	})
}

// versionedDependencyRepo shares only package construction and native repository
// setup. The package states are also used by the build test's transitive fixture.
func versionedDependencyRepo(ctx context.Context, t *testing.T, client gwclient.Client, cfg testLinuxConfig, pkgName string) (llb.State, []llb.State, string) {
	t.Helper()

	sr := newSolveRequest(withBuildTarget(cfg.Target.Worker), withSpec(ctx, t, nil))
	worker := reqToState(ctx, client, sr, t)
	pg := dalec.ProgressGroup("Get worker")
	pkgs := versionedDependencyPackages(ctx, t, client, cfg, pkgName, nil)
	repoPath := filepath.Join("/opt/repo", createRepoSuffix())
	worker = worker.With(cfg.Worker.CreateRepo(llb.Merge(pkgs, pg), repoPath))

	return worker, pkgs, repoPath
}

func versionedDependencyPackages(ctx context.Context, t *testing.T, client gwclient.Client, cfg testLinuxConfig, pkgName string, configure func(*dalec.Spec)) []llb.State {
	t.Helper()

	pg := dalec.ProgressGroup("Get worker")
	versions := []string{"1.1.1", "1.1.6", "1.1.9", "1.2.0", "1.3.0"}
	debRepo := strings.HasSuffix(cfg.Target.Package, "/deb")
	var pkgs []llb.State

	for i, version := range versions {
		depSpec := &dalec.Spec{
			Name:        pkgName,
			Version:     version,
			Revision:    "1",
			Description: "A build dependency with multiple available versions",
			License:     "MIT",
			Sources: map[string]dalec.Source{
				"dalec-test-version": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "#!/usr/bin/env bash\necho version: " + version,
							Permissions: 0o755,
						},
					},
				},
			},
			Artifacts: dalec.Artifacts{
				Binaries: map[string]dalec.ArtifactConfig{
					"dalec-test-version": {},
				},
			},
		}

		if configure != nil {
			configure(depSpec)
		}

		sr := newSolveRequest(withSpec(ctx, t, depSpec), withBuildTarget(cfg.Target.Package))
		pkg := reqToState(ctx, client, sr, t)
		if debRepo {
			// Numbered directories give apt-ftparchive a descending version index,
			// exposing selection of an older allowed patch. RPM repo helpers
			// instead require the original RPMS and SRPMS directory layout.
			pkg = llb.Scratch().File(llb.Copy(pkg, "/", fmt.Sprintf("/%d", len(versions)-i), dalec.WithCreateDestPath()), pg)
		}

		pkgs = append(pkgs, pkg)
	}

	return pkgs
}

func versionedDependencyRepoConfig(cfg testLinuxConfig, repoPath string) dalec.PackageRepositoryConfig {
	name := "bounded.repo"
	config := "[bounded]\nname=Bounded dependency fixture\nbaseurl=file://" + repoPath + "\ngpgcheck=0\nrepo_gpgcheck=0\npriority=0\nenabled=1\nmetadata_expire=0\n"
	if strings.HasSuffix(cfg.Target.Package, "/deb") {
		name = "bounded.list"
		config = "deb [trusted=yes] copy:" + repoPath + "/ /\n"
	}

	return dalec.PackageRepositoryConfig{
		Config: map[string]dalec.Source{
			name: {Inline: &dalec.SourceInline{File: &dalec.SourceInlineFile{Contents: config}}},
		},
		// Installation/test roots do not inherit repository data from the worker.
		Data: []dalec.SourceMount{{
			Dest: repoPath,
			Spec: dalec.Source{Context: &dalec.SourceContext{Name: "bounded-fixture-worker"}, Path: repoPath},
		}},
	}
}
