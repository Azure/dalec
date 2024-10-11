package test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend/windows"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	moby_buildkit_v1_frontend "github.com/moby/buildkit/frontend/gateway/pb"
)

func TestWindows(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	testWindows(ctx, t, "windowscross/container")

	t.Run("custom worker", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)
		testCustomWindowscrossWorker(ctx, t, targetConfig{
			Container: "windowscross/container",
			// The way the test uses the package target is to generate a package which
			// it then feeds back into a custom repo and adds that package as a build dep
			// to another package.
			// We don't build system packages for the windowscross base image.
			// There's also no .deb support (currently)
			// So... use a mariner2 rpm and then in CreateRepo, convert the rpm to a deb package
			// which we'll use to create the repo...
			// We can switch to this jammy/deb when that is available.
			Package: "mariner2/rpm",
			Worker:  "windowscross/worker",
		}, workerConfig{
			ContextName: windows.WindowscrossWorkerContextName,
			CreateRepo: func(pkg llb.State) llb.StateOption {
				return func(in llb.State) llb.State {
					dt := []byte(`
deb [trusted=yes] copy:/tmp/repo /
`)

					repo := in.
						Run(
							dalec.ShArgs("apt-get update && apt-get install -y apt-utils alien"),
							dalec.WithMountedAptCache("test-windowscross"),
						).
						Run(
							llb.Dir("/tmp/repo"),
							dalec.ShArgs("set -e; for i in ./RPMS/*/*.rpm; do alien --to-deb \"$i\"; done; rm -rf ./RPMS; rm -rf ./SRPMS; apt-ftparchive packages . | gzip -1 > Packages.gz"),
						).
						AddMount("/tmp/repo", pkg)

					return in.
						File(llb.Mkfile("/etc/apt/sources.list.d/windowscross.list", 0o644, dt)).
						File(llb.Copy(repo, "/", "/tmp/repo"))
				}
			},
		})
	})
}

// Windows is only supported on amd64 (ie there is no arm64 windows image currently)
// This allows the test to run on arm64 machines.
// I looked at having a good way to skip the test on non-amd64 and it all ends up
// being a bit janky and error prone.
// I'd rather just let the test run since it will work when we set an explicit platform
func withWindowsAmd64(cfg *newSolveRequestConfig) {
	if cfg.req.FrontendOpt == nil {
		cfg.req.FrontendOpt = make(map[string]string)
	}
	cfg.req.FrontendOpt["platform"] = "windows/amd64"
}

func testWindows(ctx context.Context, t *testing.T, buildTarget string) {
	t.Run("Fail when non-zero exit code during build", func(t *testing.T) {
		t.Parallel()
		spec := dalec.Spec{
			Name:        "test-build-commands-fail",
			Version:     "0.0.1",
			Revision:    "1",
			License:     "MIT",
			Website:     "https://github.com/azure/dalec",
			Vendor:      "Dalec",
			Packager:    "Dalec",
			Description: "Testing builds commands that fail cause the whole build to fail",
			Build: dalec.ArtifactBuild{
				Steps: []dalec.BuildStep{
					{
						Command: "exit 42",
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
			sr := newSolveRequest(withSpec(ctx, t, &spec), withBuildTarget(buildTarget), withWindowsAmd64)
			sr.Evaluate = true
			_, err := gwc.Solve(ctx, sr)
			var xErr *moby_buildkit_v1_frontend.ExitError
			if !errors.As(err, &xErr) {
				t.Fatalf("expected exit error, got %T: %v", errors.Unwrap(err), err)
			}
		})
	})

	t.Run("should not have internet access during build", func(t *testing.T) {
		t.Parallel()
		spec := dalec.Spec{
			Name:        "test-no-internet-access",
			Version:     "0.0.1",
			Revision:    "1",
			License:     "MIT",
			Website:     "https://github.com/azure/dalec",
			Vendor:      "Dalec",
			Packager:    "Dalec",
			Description: "Should not have internet access during build",
			Build: dalec.ArtifactBuild{
				Steps: []dalec.BuildStep{
					{
						Command: fmt.Sprintf("curl --head -ksSf %s > /dev/null", externalTestHost),
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
			sr := newSolveRequest(withSpec(ctx, t, &spec), withBuildTarget(buildTarget), withWindowsAmd64)
			sr.Evaluate = true

			_, err := gwc.Solve(ctx, sr)
			var xErr *moby_buildkit_v1_frontend.ExitError
			if !errors.As(err, &xErr) {
				t.Fatalf("expected exit error, got %T: %v", errors.Unwrap(err), err)
			}
		})
	})
	t.Run("container", func(t *testing.T) {
		spec := dalec.Spec{
			Name:        "test-container-build",
			Version:     "0.0.1",
			Revision:    "1",
			License:     "MIT",
			Website:     "https://github.com/azure/dalec",
			Vendor:      "Dalec",
			Packager:    "Dalec",
			Description: "Testing container target",
			Sources: map[string]dalec.Source{
				"src1": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "#!/usr/bin/env bash\necho hello world",
							Permissions: 0o700,
						},
					},
				},
				"src2": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"file1": {Contents: "file1 contents\n"},
							},
						},
					},
				},
				"src2-patch1": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents: `
diff --git a/file1 b/file1
index 84d55c5..22b9b11 100644
--- a/file1
+++ b/file1
@@ -1 +1 @@
-file1 contents
+file1 contents patched
`,
						},
					},
				},
				"src2-patch2": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents: `
diff --git a/file2 b/file2
new file mode 100700
index 0000000..5260cb1
--- /dev/null
+++ b/file2
@@ -0,0 +1,3 @@
+#!/usr/bin/env bash
+
+echo "Added a new file"
`,
						},
					},
				},
				"src3": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "#!/usr/bin/env bash\necho goodbye",
							Permissions: 0o700,
						},
					},
				},
			},
			Patches: map[string][]dalec.PatchSpec{
				"src2": {
					{Source: "src2-patch1"},
					{Source: "src2-patch2"},
				},
			},

			Dependencies: &dalec.PackageDependencies{},

			Build: dalec.ArtifactBuild{
				Steps: []dalec.BuildStep{
					// These are "build" steps where we aren't really building things just verifying
					// that sources are in the right place and have the right permissions and content
					{
						Command: "test -x ./src1",
					},
					{
						Command: "./src1 | grep 'hello world'",
					},
					{
						// file added by patch
						Command: "test -x ./src2/file2",
					},
					{
						Command: "grep 'Added a new file' ./src2/file2",
					},
					{
						// Test that a multiline command works with env vars
						Env: map[string]string{
							"FOO": "foo",
							"BAR": "bar",
						},
						Command: `
echo "${FOO}_0" > foo0.txt
echo "${FOO}_1" > foo1.txt
echo "$BAR" > bar.txt
`,
					},
				},
			},

			Image: &dalec.ImageConfig{
				Post: &dalec.PostInstall{
					Symlinks: map[string]dalec.SymlinkTarget{
						"/Windows/System32/src1": {Path: "/src1"},
						"/Windows/System32/src3": {Path: "/non/existing/dir/src3"},
					},
				},
			},

			Artifacts: dalec.Artifacts{
				Binaries: map[string]dalec.ArtifactConfig{
					"src1":       {},
					"src2/file2": {},
					"src3":       {},
					// These are files we created in the build step
					// They aren't really binaries but we want to test that they are created and have the right content
					"foo0.txt": {},
					"foo1.txt": {},
					"bar.txt":  {},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
			sr := newSolveRequest(withSpec(ctx, t, &spec), withBuildTarget(buildTarget), withWindowsAmd64)
			sr.Evaluate = true
			res := solveT(ctx, t, gwc, sr)

			ref, err := res.SingleRef()
			if err != nil {
				t.Fatal(err)
			}

			post := spec.GetImagePost("windowscross")
			for srcPath, l := range post.Symlinks {
				b1, err := ref.ReadFile(ctx, gwclient.ReadRequest{
					Filename: srcPath,
				})
				if err != nil {
					t.Fatalf("couldn't find Windows \"symlink\" target %q: %v", srcPath, err)
				}

				b2, err := ref.ReadFile(ctx, gwclient.ReadRequest{
					Filename: l.Path,
				})
				if err != nil {
					t.Fatalf("couldn't find Windows \"symlink\" at destination %q: %v", l.Path, err)
				}

				if len(b1) != len(b2) {
					t.Fatalf("Windows \"symlink\" not identical to target file")
				}

				for i := range b1 {
					if b1[i] != b2[i] {
						t.Fatalf("Windows \"symlink\" not identical to target file")
					}
				}
			}
		})
	})

	t.Run("test signing", windowsSigningTests)

	t.Run("go module", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)

		spec := &dalec.Spec{
			Name:        "test-build-with-gomod",
			Version:     "0.0.1",
			Revision:    "1",
			License:     "MIT",
			Website:     "https://github.com/azure/dalec",
			Vendor:      "Dalec",
			Packager:    "Dalec",
			Description: "Testing container target",
			Sources: map[string]dalec.Source{
				"src": {
					Generate: []*dalec.SourceGenerator{
						{
							Gomod: &dalec.GeneratorGomod{},
						},
					},
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"main.go": {Contents: gomodFixtureMain},
								"go.mod":  {Contents: gomodFixtureMod},
								"go.sum":  {Contents: gomodFixtureSum},
							},
						},
					},
				},
			},
			Dependencies: &dalec.PackageDependencies{
				Build: map[string]dalec.PackageConstraints{
					// TODO: This works at least for now, but is distro specific and
					// could break on new distros (though that is still unlikely).
					"golang": {},
				},
			},
			Build: dalec.ArtifactBuild{
				Steps: []dalec.BuildStep{
					{Command: "[ -d \"${GOMODCACHE}/github.com/cpuguy83/tar2go@v0.3.1\" ]"},
					{Command: "[ -d ./src ]"},
					{Command: "[ -f ./src/main.go ]"},
					{Command: "[ -f ./src/go.mod ]"},
					{Command: "[ -f ./src/go.sum ]"},
					{Command: "cd ./src && go build"},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			req := newSolveRequest(withBuildTarget(buildTarget), withSpec(ctx, t, spec), withWindowsAmd64)
			solveT(ctx, t, client, req)
		})
	})

	t.Run("test image configs", func(t *testing.T) {
		t.Parallel()

		ctx := startTestSpan(baseCtx, t)
		testImageConfig(ctx, t, buildTarget, withWindowsAmd64)
	})
}

func runBuild(ctx context.Context, t *testing.T, gwc gwclient.Client, spec *dalec.Spec, srOpts ...srOpt) {
	st := prepareSigningState(ctx, t, gwc, spec, srOpts...)

	def, err := st.Marshal(ctx)
	if err != nil {
		t.Fatal(err)
	}

	res := solveT(ctx, t, gwc, gwclient.SolveRequest{
		Definition: def.ToPB(),
	})

	verifySigning(ctx, t, res)
}

func verifySigning(ctx context.Context, t *testing.T, res *gwclient.Result) {
	tgt := readFile(ctx, t, "/target", res)
	cfg := readFile(ctx, t, "/config.json", res)

	if string(tgt) != "windowscross" {
		t.Fatal(fmt.Errorf("target incorrect; either not sent to signer or not received back from signer"))
	}

	if !strings.Contains(string(cfg), "windows") {
		t.Fatal(fmt.Errorf("configuration incorrect"))
	}
}

func prepareSigningState(ctx context.Context, t *testing.T, gwc gwclient.Client, spec *dalec.Spec, extraSrOpts ...srOpt) llb.State {
	zipper := getZipperState(ctx, t, gwc)

	srOpts := []srOpt{withSpec(ctx, t, spec), withBuildTarget("windowscross/zip"), withWindowsAmd64}
	srOpts = append(srOpts, extraSrOpts...)

	sr := newSolveRequest(srOpts...)
	st := reqToState(ctx, gwc, sr, t)
	st = zipper.Run(llb.Args([]string{"bash", "-c", `for f in ./*.zip; do unzip "$f"; done`}), llb.Dir("/tmp/mnt")).
		AddMount("/tmp/mnt", st)
	return st
}

func getZipperState(ctx context.Context, t *testing.T, gwc gwclient.Client) llb.State {
	zipperSpec := fillMetadata("bar", &dalec.Spec{
		Dependencies: &dalec.PackageDependencies{
			Runtime: map[string]dalec.PackageConstraints{
				"unzip": {},
			},
		},
	})

	sr := newSolveRequest(withSpec(ctx, t, zipperSpec), withBuildTarget("mariner2/container"))
	zipper := reqToState(ctx, gwc, sr, t)
	return zipper
}

func fillMetadata(fakename string, s *dalec.Spec) *dalec.Spec {
	s.Name = fakename
	s.Version = "0.0.1"
	s.Description = "foo bar baz"
	s.Website = "https://foo.bar.baz"
	s.Revision = "1"
	s.License = "MIT"
	s.Vendor = "nothing"
	s.Packager = "Bill Spummins"

	return s
}

func testCustomWindowscrossWorker(ctx context.Context, t *testing.T, targetCfg targetConfig, workerCfg workerConfig) {
	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
		// base package that will be used as a build dependency of the main package.
		depSpec := &dalec.Spec{
			Name:        "dalec-test-package",
			Version:     "0.0.1",
			Revision:    "1",
			Description: "A basic package for various testing uses",
			License:     "MIT",
			Sources: map[string]dalec.Source{
				"hello.txt": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents: "hello world!",
						},
					},
				},
			},
			Artifacts: dalec.Artifacts{
				Docs: map[string]dalec.ArtifactConfig{
					"hello.txt": {},
				},
			},
		}

		// Main package, this should fail to build without a custom worker that has
		// the base package available.
		spec := &dalec.Spec{
			Name:        "test-dalec-custom-worker",
			Version:     "0.0.1",
			Revision:    "1",
			Description: "Testing allowing custom worker images to be provided",
			License:     "MIT",
			Dependencies: &dalec.PackageDependencies{
				Build: map[string]dalec.PackageConstraints{
					depSpec.Name: {},
				},
			},
		}

		// Make sure the built-in worker can't build this package
		sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(targetCfg.Container), withWindowsAmd64)
		_, err := gwc.Solve(ctx, sr)
		if err == nil {
			t.Fatal("expected solve to fail")
		}

		var xErr *moby_buildkit_v1_frontend.ExitError
		if !errors.As(err, &xErr) {
			t.Fatalf("got unexpected error, expected error type %T: %v", xErr, err)
		}

		// Build the base package
		sr = newSolveRequest(withSpec(ctx, t, depSpec), withBuildTarget(targetCfg.Package))
		pkg := reqToState(ctx, gwc, sr, t)

		// Build the worker target, this will give us the worker image as an output.
		// Note: Currently we need to provide a dalec spec just due to how the router is setup.
		//       The spec can be nil, though, it just needs to be parsable by yaml unmarshaller.
		sr = newSolveRequest(withBuildTarget(targetCfg.Worker), withSpec(ctx, t, nil))
		worker := reqToState(ctx, gwc, sr, t)

		// Add the base package + repo to the worker
		// This should make it so when dalec installs build deps it can use the package
		// we built above.
		worker = worker.With(workerCfg.CreateRepo(pkg))

		// Now build again with our custom worker
		// Note, we are solving the main spec, not depSpec here.
		sr = newSolveRequest(withSpec(ctx, t, spec), withBuildContext(ctx, t, workerCfg.ContextName, worker), withBuildTarget(targetCfg.Container), withWindowsAmd64)
		solveT(ctx, t, gwc, sr)

		// TODO: we should have a test to make sure this also works with source policies.
		// Unfortunately it seems like there is an issue with the gateway client passing
		// in source policies.
	})
}
