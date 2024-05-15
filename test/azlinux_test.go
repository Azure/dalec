package test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/Azure/dalec"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	moby_buildkit_v1_frontend "github.com/moby/buildkit/frontend/gateway/pb"
)

func TestMariner2(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	testLinuxDistro(ctx, t, "mariner2/container", "mariner2/rpm")
}

func TestAzlinux3(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	testLinuxDistro(ctx, t, "azlinux3/container", "azlinux3/rpm")
}

func mustParse(t *testing.T, spec string) *dalec.Spec {
	t.Helper()
	s, err := dalec.LoadSpec([]byte(spec))
	if err != nil {
		t.Fatalf("failed to parse spec: %v", err)
	}
	return s
}

func testLinuxDistro(ctx context.Context, t *testing.T, buildTarget string, signTarget string) {
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

		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) (*gwclient.Result, error) {
			sr := newSolveRequest(withSpec(ctx, t, &spec), withBuildTarget(buildTarget))
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
			Version:     "0.0.1",
			Revision:    "1",
			License:     "MIT",
			Website:     "https://github.com/azure/dalec",
			Vendor:      "Dalec",
			Packager:    "Dalec",
			Description: "Should not have internet access during build",
			Dependencies: &dalec.PackageDependencies{
				Runtime: map[string][]string{"curl": {}},
			},
			Build: dalec.ArtifactBuild{
				Steps: []dalec.BuildStep{
					{
						Command: fmt.Sprintf("curl --head -ksSf %s > /dev/null", externalTestHost),
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) (*gwclient.Result, error) {
			sr := newSolveRequest(withSpec(ctx, t, &spec), withBuildTarget(buildTarget))
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
			},
			Patches: map[string][]dalec.PatchSpec{
				"src2": {
					{Source: "src2-patch1"},
					{Source: "src2-patch2"},
				},
			},

			Dependencies: &dalec.PackageDependencies{
				Runtime: map[string][]string{
					"bash":      {},
					"coreutils": {},
				},
			},

			Build: dalec.ArtifactBuild{
				Steps: []dalec.BuildStep{
					// These are "build" steps where we aren't really building things just verifying
					// that sources are in the right place and have the right permissions and content
					{
						// file added by patch
						Command: "test -f ./src1",
					},
					{
						Command: "test -x ./src1",
					},
					{
						Command: "test ! -d ./src1",
					},
					{
						Command: "./src1 | grep 'hello world'",
					},
					{
						// file added by patch
						Command: "ls -lh ./src2/file2",
					},
					{
						// file added by patch
						Command: "test -f ./src2/file2",
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
						"/usr/bin/src1": {Path: "/src1"},
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

			Tests: []*dalec.TestSpec{
				{
					Name: "Check that the binary artifacts execute and provide the expected output",
					Steps: []dalec.TestStep{
						{
							Command: "/usr/bin/src1",
							Stdout:  dalec.CheckOutput{Equals: "hello world\n"},
							Stderr:  dalec.CheckOutput{Empty: true},
						},
						{
							Command: "/usr/bin/file2",
							Stdout:  dalec.CheckOutput{Equals: "Added a new file\n"},
							Stderr:  dalec.CheckOutput{Empty: true},
						},
					},
				},
				{
					Name: "Check that multi-line command (from build step) with env vars propagates env vars to whole command",
					Files: map[string]dalec.FileCheckOutput{
						"/usr/bin/foo0.txt": {CheckOutput: dalec.CheckOutput{StartsWith: "foo_0\n"}},
						"/usr/bin/foo1.txt": {CheckOutput: dalec.CheckOutput{StartsWith: "foo_1\n"}},
						"/usr/bin/bar.txt":  {CheckOutput: dalec.CheckOutput{StartsWith: "bar\n"}},
					},
				},
				{
					Name: "Post-install symlinks should be created",
					Files: map[string]dalec.FileCheckOutput{
						"/src1": {},
					},
					Steps: []dalec.TestStep{
						{Command: "/bin/bash -c 'test -L /src1'"},
						{Command: "/bin/bash -c 'test \"$(readlink /src1)\" = \"/usr/bin/src1\"'"},
						{Command: "/src1", Stdout: dalec.CheckOutput{Equals: "hello world\n"}, Stderr: dalec.CheckOutput{Empty: true}},
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) (*gwclient.Result, error) {
			// Make sure the test framework was actually executed by the build target.
			// This appends a test case so that is expected to fail and as such cause the build to fail.
			spec.Tests = append(spec.Tests, &dalec.TestSpec{
				Name: "Test framework should be executed",
				Steps: []dalec.TestStep{
					{Command: "/bin/sh -c 'echo this command should fail; exit 42'"},
				},
			})

			sr := newSolveRequest(withSpec(ctx, t, &spec), withBuildTarget(buildTarget))
			sr.Evaluate = true

			if _, err := gwc.Solve(ctx, sr); err == nil {
				return nil, fmt.Errorf("expected test spec to run with error but got none")
			}

			return gwclient.NewResult(), nil
		})
	})

	runTest := func(t *testing.T, f gwclient.BuildFunc) {
		t.Helper()
		ctx := startTestSpan(baseCtx, t)
		testEnv.RunTest(ctx, t, f)
	}

	t.Run("test signing", func(t *testing.T) {
		t.Parallel()
		spec := dalec.Spec{
			Name:        "foo",
			Version:     "v0.0.1",
			Description: "foo bar baz",
			Website:     "https://foo.bar.baz",
			Revision:    "1",
			PackageConfig: &dalec.PackageConfig{
				Signer: &dalec.Frontend{
					Image: phonySignerRef,
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
		}

		runTest(t, distroSigningTest(t, &spec, signTarget))
	})

	t.Run("test systemd unit", func(t *testing.T) {
		t.Parallel()
		spec := &dalec.Spec{
			Name:        "test-systemd-unit",
			Description: "Test systemd unit",
			Website:     "https://www.github.com/Azure/dalec",
			Version:     "0.0.1",
			Revision:    "1",
			Vendor:      "Microsoft",
			License:     "Apache 2.0",
			Packager:    "Microsoft <support@microsoft.com>",
			Targets: map[string]dalec.Target{
				"mariner2": {
					Image: &dalec.ImageConfig{
						Base: "mcr.microsoft.com/cbl-mariner/base/core:2.0",
					},
				},
			},
			Dependencies: &dalec.PackageDependencies{
				Build: map[string][]string{
					"msft-golang": {},
				},
			},
			Sources: map[string]dalec.Source{
				"src": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{

							Files: map[string]*dalec.SourceInlineFile{
								"simple.service": {
									Contents: `,
[Unit]
Description=Phony Service
After=network.target

[Service]
Type=simple
ExecStart=/usr/bin/service
Restart=always

[Install]
WantedBy=multi-user.target
`},
							},
						},
					},
				},
			},
			Artifacts: dalec.Artifacts{
				Services: map[string]dalec.ServiceConfig{
					"src/simple.service": {},
				},
			},
			Tests: []*dalec.TestSpec{
				{
					Name: "Check service files",
					Files: map[string]dalec.FileCheckOutput{
						"/usr/lib/systemd/system/simple.service": {
							CheckOutput: dalec.CheckOutput{Contains: []string{"ExecStart=/usr/bin/service"}},
							Permissions: 0644,
						},
						"/usr/lib/systemd/system-preset/simple.preset": {
							CheckOutput: dalec.CheckOutput{Contains: []string{"enable simple.service"}},
							Permissions: 0644,
						},
					},
				},
			},
		}

		/*
		   tests:
		    - name: Check service files
		      files:
		         /usr/lib/systemd/system/simple.service:
		           permissions: 0644
		           contains:
		           - "ExecStart=/usr/bin/service"
		         /usr/lib/systemd/system-preset/simple.preset:
		             permissions: 0644
		           contains:
		               - "enable simple.service"
		*/
		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
			req := newSolveRequest(withBuildTarget(buildTarget), withSpec(ctx, t, spec))
			return client.Solve(ctx, req)
		})

		// Test to ensure disabling works
		spec.Artifacts.Services["src/simple.service"] = dalec.ServiceConfig{
			Disable: true,
		}
		spec.Tests = []*dalec.TestSpec{
			{
				Name: "Check service files",
				Files: map[string]dalec.FileCheckOutput{
					"/usr/lib/systemd/system/simple.service": {
						CheckOutput: dalec.CheckOutput{Contains: []string{"ExecStart=/usr/bin/service"}},
						Permissions: 0644,
					},
					"/usr/lib/systemd/system-preset/simple.preset": {
						// This is the only change from the previous test, service should be
						// disabled in preset
						CheckOutput: dalec.CheckOutput{Contains: []string{"disable simple.service"}},
						Permissions: 0644,
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
			req := newSolveRequest(withBuildTarget(buildTarget), withSpec(ctx, t, spec))
			return client.Solve(ctx, req)
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
			req := newSolveRequest(withBuildTarget(buildTarget), withSpec(ctx, t, spec))
			return client.Solve(ctx, req)
		})

	})

	t.Run("test directory creation", func(t *testing.T) {
		t.Parallel()
		spec := &dalec.Spec{
			Name:        "test-directory-creation",
			Version:     "v0.0.1",
			Revision:    "1",
			License:     "MIT",
			Website:     "https://github.com/azure/dalec",
			Vendor:      "Dalec",
			Packager:    "Dalec",
			Description: "Should Create Specified Directories",
			Dependencies: &dalec.PackageDependencies{
				Runtime: map[string][]string{"curl": {}},
			},
			Sources: map[string]dalec.Source{
				"src1": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "#!/usr/bin/env bash\necho hello world",
							Permissions: 0o700,
						},
					},
				},
			},
			Build: dalec.ArtifactBuild{},
			Artifacts: dalec.Artifacts{
				Binaries: map[string]dalec.ArtifactConfig{
					"src1": {},
				},
				Directories: &dalec.CreateArtifactDirectories{
					Config: map[string]dalec.ArtifactDirConfig{
						"test": {},
						"testWithPerms": {
							Mode: 0o700,
						},
					},
					State: map[string]dalec.ArtifactDirConfig{
						"one/with/slashes": {},
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
			req := newSolveRequest(withBuildTarget(buildTarget), withSpec(ctx, t, spec))
			res, err := client.Solve(ctx, req)
			if err != nil {
				return nil, err
			}

			ref, err := res.SingleRef()
			if err != nil {
				return nil, err
			}

			if err := validatePathAndPermissions(ctx, ref, "/etc/test", 0o755); err != nil {
				return nil, err
			}
			if err := validatePathAndPermissions(ctx, ref, "/etc/testWithPerms", 0o700); err != nil {
				return nil, err
			}
			if err := validatePathAndPermissions(ctx, ref, "/var/lib/one/with/slashes", 0o755); err != nil {
				return nil, err
			}
			return gwclient.NewResult(), nil
		})
	})

	t.Run("test config files handled", func(t *testing.T) {
		t.Parallel()
		spec := &dalec.Spec{
			Name:        "test-config-files-work",
			Version:     "v0.0.1",
			Revision:    "1",
			License:     "MIT",
			Website:     "https://github.com/azure/dalec",
			Vendor:      "Dalec",
			Packager:    "Dalec",
			Description: "Should Create Specified Directories",
			Dependencies: &dalec.PackageDependencies{
				Runtime: map[string][]string{"curl": {}},
			},
			Sources: map[string]dalec.Source{
				"src1": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "message=hello",
							Permissions: 0o700,
						},
					},
				},
				"src2": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "message=goodbye",
							Permissions: 0o700,
						},
					},
				},
			},
			Build: dalec.ArtifactBuild{},
			Artifacts: dalec.Artifacts{
				ConfigFiles: map[string]dalec.ArtifactConfig{
					"src1": {},
					"src2": {
						SubPath: "sysconfig",
					},
				},
			},
			Tests: []*dalec.TestSpec{
				{
					Name: "Config Files Should Be Created in correct place",
					Files: map[string]dalec.FileCheckOutput{
						"/etc/src1":           {},
						"/etc/sysconfig/src2": {},
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
			sr := newSolveRequest(withBuildTarget(buildTarget), withSpec(ctx, t, spec))
			sr.Evaluate = true
			_, err := client.Solve(ctx, sr)
			if err != nil {
				return nil, fmt.Errorf("unable to build package with config files %w", err)
			}
			return gwclient.NewResult(), nil
		})
	})
}

func validatePathAndPermissions(ctx context.Context, ref gwclient.Reference, path string, expected os.FileMode) error {
	stat, err := ref.StatFile(ctx, gwclient.StatRequest{Path: path})
	if err != nil {
		return err
	}

	got := os.FileMode(stat.Mode).Perm()

	if expected != got {
		return fmt.Errorf("expected permissions %v to equal expected %v", got, expected)
	}
	return nil
}
