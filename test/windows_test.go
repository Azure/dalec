package test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Azure/dalec"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	moby_buildkit_v1_frontend "github.com/moby/buildkit/frontend/gateway/pb"
)

func TestWindows(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	testWindows(ctx, t, "windowscross/container")
}

func testWindows(ctx context.Context, t *testing.T, buildTarget string) {
	// Windows is only supported on amd64 (ie there is no arm64 windows image currently)
	// This allows the test to run on arm64 machines.
	// I looked at having a good way to skip the test on non-amd64 and it all ends up
	// being a bit janky and error prone.
	// I'd rather just let the test run since it will work when we set an explicit platform
	withAmd64Platform := func(sr *gwclient.SolveRequest) {
		if sr.FrontendOpt == nil {
			sr.FrontendOpt = make(map[string]string)
		}
		sr.FrontendOpt["platform"] = "windows/amd64"
	}

	t.Run("Fail when non-zero exit code during build", func(t *testing.T) {
		t.Parallel()
		spec := dalec.Spec{
			Name:        "test-build-commands-fail",
			Version:     "v0.0.1",
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

		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) (*gwclient.Result, error) {
			sr := newSolveRequest(withSpec(ctx, t, &spec), withBuildTarget(buildTarget), withAmd64Platform)
			sr.Evaluate = true
			_, err := gwc.Solve(ctx, sr)
			var xErr *moby_buildkit_v1_frontend.ExitError
			if !errors.As(err, &xErr) {
				t.Fatalf("expected exit error, got %T: %v", errors.Unwrap(err), err)
			}
			return gwclient.NewResult(), nil
		})
	})

	t.Run("should not have internet access during build", func(t *testing.T) {
		t.Parallel()
		spec := dalec.Spec{
			Name:        "test-no-internet-access",
			Version:     "v0.0.1",
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

		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) (*gwclient.Result, error) {
			sr := newSolveRequest(withSpec(ctx, t, &spec), withBuildTarget(buildTarget), withAmd64Platform)
			sr.Evaluate = true

			_, err := gwc.Solve(ctx, sr)
			var xErr *moby_buildkit_v1_frontend.ExitError
			if !errors.As(err, &xErr) {
				t.Fatalf("expected exit error, got %T: %v", errors.Unwrap(err), err)
			}
			return gwclient.NewResult(), nil
		})
	})
	t.Run("container", func(t *testing.T) {
		spec := dalec.Spec{
			Name:        "test-container-build",
			Version:     "v0.0.1",
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
					},
				},
			},

			Artifacts: dalec.Artifacts{
				Binaries: map[string]dalec.ArtifactConfig{
					"src1":       {},
					"src2/file2": {},
					// These are files we created in the build step
					// They aren't really binaries but we want to test that they are created and have the right content
					"foo0.txt": {},
					"foo1.txt": {},
					"bar.txt":  {},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) (*gwclient.Result, error) {
			sr := newSolveRequest(withSpec(ctx, t, &spec), withBuildTarget(buildTarget), withAmd64Platform)
			sr.Evaluate = true
			res, err := gwc.Solve(ctx, sr)
			if err != nil {
				return nil, err
			}

			ref, err := res.SingleRef()
			if err != nil {
				return nil, err
			}

			for srcPath, l := range spec.GetSymlinks("windowscross") {
				b1, err := ref.ReadFile(ctx, gwclient.ReadRequest{
					Filename: srcPath,
				})
				if err != nil {
					return nil, fmt.Errorf("couldn't find Windows \"symlink\" target %q: %w", srcPath, err)
				}

				b2, err := ref.ReadFile(ctx, gwclient.ReadRequest{
					Filename: l.Path,
				})
				if err != nil {
					return nil, fmt.Errorf("couldn't find Windows \"symlink\" at destination %q: %w", l.Path, err)
				}

				if len(b1) != len(b2) {
					return nil, fmt.Errorf("Windows \"symlink\" not identical to target file")
				}

				for i := range b1 {
					if b1[i] != b2[i] {
						return nil, fmt.Errorf("Windows \"symlink\" not identical to target file")
					}
				}
			}

			return gwclient.NewResult(), nil
		})
	})

	runTest := func(t *testing.T, f gwclient.BuildFunc) {
		t.Helper()
		ctx := startTestSpan(baseCtx, t)
		testEnv.RunTest(ctx, t, f)
	}

	t.Run("test windows signing", func(t *testing.T) {
		t.Parallel()
		runTest(t, func(ctx context.Context, gwc gwclient.Client) (*gwclient.Result, error) {
			spec := fillMetadata("foo", &dalec.Spec{
				Targets: map[string]dalec.Target{
					"windowscross": {
						PackageConfig: &dalec.PackageConfig{
							Signer: &dalec.Frontend{
								Image: phonySignerRef,
							},
						},
					},
				},
				Sources: map[string]dalec.Source{
					"foo": {
						Inline: &dalec.SourceInline{
							File: &dalec.SourceInlineFile{
								Contents: "#!/usr/bin/env bash\necho \"hello, world!\"\n",
							},
						},
					},
				},
				Build: dalec.ArtifactBuild{
					Steps: []dalec.BuildStep{
						{
							Command: "/bin/true",
						},
					},
				},
				Artifacts: dalec.Artifacts{
					Binaries: map[string]dalec.ArtifactConfig{
						"foo": {},
					},
				},
			})

			zipperSpec := fillMetadata("bar", &dalec.Spec{
				Dependencies: &dalec.PackageDependencies{
					Runtime: map[string][]string{
						"unzip": {},
					},
				},
			})

			sr := newSolveRequest(withSpec(ctx, t, zipperSpec), withBuildTarget("mariner2/container"), withAmd64Platform)
			zipper := reqToState(ctx, gwc, sr, t)

			sr = newSolveRequest(withSpec(ctx, t, spec), withBuildTarget("windowscross/zip"))
			st := reqToState(ctx, gwc, sr, t)

			st = zipper.Run(llb.Args([]string{"bash", "-c", `for f in ./*.zip; do unzip "$f"; done`}), llb.Dir("/tmp/mnt")).
				AddMount("/tmp/mnt", st)

			def, err := st.Marshal(ctx)
			if err != nil {
				t.Fatal(err)
			}

			res, err := gwc.Solve(ctx, gwclient.SolveRequest{
				Definition: def.ToPB(),
			})
			if err != nil {
				t.Fatal(err)
			}

			tgt := readFile(ctx, t, "/target", res)
			cfg := readFile(ctx, t, "/config.json", res)

			if string(tgt) != "windowscross" {
				t.Fatal(fmt.Errorf("target incorrect; either not sent to signer or not received back from signer"))
			}

			if !strings.Contains(string(cfg), "windows") {
				t.Fatal(fmt.Errorf("configuration incorrect"))
			}

			return gwclient.NewResult(), nil
		})
	})

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
				Build: map[string][]string{
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

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
			req := newSolveRequest(withBuildTarget(buildTarget), withSpec(ctx, t, spec), withAmd64Platform)
			return client.Solve(ctx, req)
		})
	})
}

func reqToState(ctx context.Context, gwc gwclient.Client, sr gwclient.SolveRequest, t *testing.T) llb.State {
	res, err := gwc.Solve(ctx, sr)
	if err != nil {
		t.Fatal(err)
	}

	ref, err := res.SingleRef()
	if err != nil {
		t.Fatal(err)
	}

	st, err := ref.ToState()
	if err != nil {
		t.Fatal(err)
	}

	return st
}

func fillMetadata(fakename string, s *dalec.Spec) *dalec.Spec {
	s.Name = "bar"
	s.Version = "v0.0.1"
	s.Description = "foo bar baz"
	s.Website = "https://foo.bar.baz"
	s.Revision = "1"
	s.License = "MIT"
	s.Vendor = "nothing"
	s.Packager = "Bill Spummins"

	return s
}
